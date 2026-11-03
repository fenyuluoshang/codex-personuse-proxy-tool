"""Apply reviewed source transforms to a disposable fixed upstream checkout.

Never run this against the submodule: build-proxy-manager.ps1 exports a copy.
The exact anchors intentionally fail closed if upstream has drifted.
"""
from pathlib import Path
import sys


def replace_once(text, old, new):
    if text.count(old) != 1:
        raise RuntimeError("upstream source anchor did not match exactly once")
    return text.replace(old, new, 1)


def block(text, start, stop, replacement):
    if text.count(start) != 1 or text.count(stop) != 1:
        raise RuntimeError("upstream block anchors did not match exactly once")
    left = text.index(start)
    right = text.index(stop, left)
    return text[:left] + replacement + text[right:]


def apply(root):
    if (root / ".git").exists() or not (root / ".codex-proxy-isolated-source.json").is_file():
        raise RuntimeError("patches may only be applied to a marked isolated export")
    src = root / "src"
    core = (src / "Core.cs").read_text(encoding="utf-8-sig")
    core = block(core, "    public static class SystemProxyHelper\n", "    internal static class Native\n", "")
    core = block(core, "        public static void CloseAll(int waitMs)\n", "        /// <summary>带代理参数启动", '''        public static void CloseAll(int waitMs)
        {
            throw new ManualRestartRequiredException();
        }

''')
    core = replace_once(core, '''                foreach (string name in EnvUtil.ProxyVarNames) EnvUtil.SetUser(name, settings.ProxyUrl);
                EnvUtil.SetUser(EnvUtil.NoProxyName, EnvUtil.DefaultNoProxy);''', '''                SafeRecovery.ApplyEnvironment(settings.ProxyUrl);''')
    core = block(core, "            bool removed = false;\n", "            RemoveIfOurs(Paths.DesktopManagedLnk", '''            SafeRecovery.RestoreEnvironment();

''')
    core = block(core, "            // 系统代理：仅当", "            try\n            {\n                if (ShellShortcut.PointsTo(Paths.DesktopAppLnk", '''            SafeRecovery.RestoreSystemProxy();

''')
    core = replace_once(core, '            LogBus.Write("还原完成，系统恢复为“不使用代理直连”的状态", LogKind.Success);',
                        '            LogBus.Write("Recovery finished. Review conflicts before assuming settings were restored.", LogKind.Info);')
    core = block(core, '            if (closeApp && Process.GetProcessesByName("ChatGPT").Length > 0)\n',
                 '            LogBus.Write("Recovery finished.', '''            if (closeApp)
                LogBus.Write("Please close the existing application manually if a restart is needed.", LogKind.Warn);
''')
    core = block(core, '''                if (!restartIfNeeded)
''', '''            }
            else if (Process.GetProcessesByName("ChatGPT").Length > 0)
''', '''                throw new ManualRestartRequiredException();
''')
    # Direct deletion/writes are no longer used. Remove their dangerous bodies to
    # make accidental future calls fail closed instead of bypassing the journal.
    core = block(core, "        public static void SetUser(string name, string value)\n", "        public static void ApplyProcessEnv(string url)\n", '''        public static void SetUser(string name, string value)
        {
            throw new InvalidOperationException("Use the journaled environment transaction.");
        }

        public static void DeleteUser(string name)
        {
            throw new InvalidOperationException("Use the journaled environment recovery.");
        }

''')
    # Opt-in system-wide changes remain available in the original UI, but are not
    # enabled merely by opening the integrated helper for the first time.
    core = replace_once(core, 'public bool OptTakeOverShortcut = true;', 'public bool OptTakeOverShortcut = false;')
    core = replace_once(core, 'public bool OptAutoLaunch = true;', 'public bool OptAutoLaunch = false;')
    core = replace_once(core, 'public bool OptSystemProxyFrame = true;', 'public bool OptSystemProxyFrame = false;')
    (src / "Core.cs").write_text(core, encoding="utf-8-sig", newline="\n")

    program = (src / "Program.cs").read_text(encoding="utf-8-sig")
    program = replace_once(program, '''            bool launchMode = false;''', '''            int safeExitCode;
            if (SafeCommand.TryHandle(args, out safeExitCode)) return safeExitCode;
            SafeRecovery.RecoverExpiredSystemProxy();
            bool launchMode = false;''')
    program = block(program, "            // 内部开关：临时系统代理窗口", "            // 命令行模式", '''            // Old unscoped watchdog arguments are intentionally unsupported.
            if (args.Length > 0 && args[0] == "--sysproxy-timeout") return 64;

''')
    program = replace_once(program, '''                Operations.Enable(settings);
                return 0;''', '''                EnableOutcome outcome = Operations.Enable(settings);
                bool succeeded = outcome.ShortcutsDone && (!settings.OptSetEnvVars || outcome.EnvDone) &&
                    (!settings.OptTakeOverShortcut || outcome.TakeOverDone) && (!settings.OptAutoLaunch || outcome.Launched);
                return succeeded ? 0 : 3;''')
    program = replace_once(program, '''                Operations.Restore(settings, false);
                return 0;''', '''                Operations.Restore(settings, false);
                return SafeCommand.RecoveryExitCode();''')
    program = block(program, '''                        MessageBoxResult answer = MessageBox.Show(
''', '''                    }
                    else
''', '''                        return "Please close the existing application manually before launching with the new proxy settings.";
''')
    (src / "Program.cs").write_text(program, encoding="utf-8-sig", newline="\n")

    build = root / "build" / "build.ps1"
    text = build.read_text(encoding="utf-8-sig")
    text = replace_once(text, "(Join-Path $fw 'System.Management.dll'),", "(Join-Path $fw 'System.Management.dll'),\n    (Join-Path $fw 'System.Security.dll'),\n    (Join-Path $fw 'System.Web.Extensions.dll'),")
    text = replace_once(text, "    (Join-Path $srcDir 'Program.cs')", "    (Join-Path $srcDir 'Program.cs'),\n    (Join-Path $srcDir 'RecoveryJournal.cs'),\n    (Join-Path $srcDir 'SafeRecovery.cs'),\n    (Join-Path $srcDir 'SystemProxyHelper.cs')")
    build.write_text(text, encoding="utf-8-sig", newline="\n")
    for name in ("RecoveryJournal.cs", "SafeRecovery.cs", "SystemProxyHelper.cs"):
        (src / name).write_text((Path(__file__).parent / name).read_text(encoding="utf-8"), encoding="utf-8-sig", newline="\n")


if __name__ == "__main__":
    apply(Path(sys.argv[1]))

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Threading;
using System.Web.Script.Serialization;
using CodexPersonalProxy;
using Microsoft.Win32;

namespace ChatGPTProxyManager
{
    internal static class SafeRecovery
    {
        private const string InternetSettings = @"Software\Microsoft\Windows\CurrentVersion\Internet Settings";
        private static readonly string Root = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "CodexPersonalProxyTool", "recovery-v1");
        private static RegistryJournal EnvironmentJournal { get { return new RegistryJournal("Environment", Path.Combine(Root, "environment.bin")); } }
        private static RegistryJournal SystemJournal { get { return new RegistryJournal(InternetSettings, Path.Combine(Root, "system-proxy.bin"), true); } }

        public static void ApplyEnvironment(string proxyUrl)
        {
            var desired = new Dictionary<string, RegistrySnapshot>(StringComparer.OrdinalIgnoreCase);
            foreach (string name in EnvUtil.ProxyVarNames) desired.Add(name, RegistrySnapshot.String(proxyUrl));
            desired.Add(EnvUtil.NoProxyName, RegistrySnapshot.String(EnvUtil.DefaultNoProxy));
            EnvironmentJournal.Apply(desired, null);
        }

        public static RecoveryResult RestoreEnvironment()
        {
            RecoveryResult result = EnvironmentJournal.Restore(false, null);
            if (result.Restored > 0) EnvUtil.Broadcast();
            Report("environment", result);
            return result;
        }

        public static void OpenSystemProxy(string host, int port, int seconds)
        {
            if (seconds < 3 || seconds > 180 || port < 1 || port > 65535 || String.IsNullOrWhiteSpace(host))
                throw new ArgumentException("Invalid temporary proxy window");
            // An unfinished previous window is recovered only when it has expired.
            RecoveryResult expired = SystemJournal.Restore(true, null);
            if (!expired.Success) throw new InvalidOperationException("System proxy recovery conflicts require review");
            RecoveryStatus status = SystemJournal.Status();
            if (status.Pending)
                throw new InvalidOperationException("Another temporary proxy window is active");
            var desired = new Dictionary<string, RegistrySnapshot>(StringComparer.OrdinalIgnoreCase) {
                { "ProxyEnable", RegistrySnapshot.DWord(1) },
                { "ProxyServer", RegistrySnapshot.String(host + ":" + port) },
                { "AutoConfigURL", new RegistrySnapshot() },
                { "AutoDetect", RegistrySnapshot.DWord(0) }
            };
            string id = SystemJournal.Apply(desired, DateTime.UtcNow.AddSeconds(seconds));
            try
            {
                var start = new ProcessStartInfo(Paths.ExePath, "--safe-watch-system " + id);
                start.UseShellExecute = false;
                start.CreateNoWindow = true;
                if (Process.Start(start) == null) throw new IOException("Recovery watchdog did not start");
            }
            catch
            {
                // The caller may be a short-lived launcher, so no in-process timer fallback.
                SystemJournal.Restore(false, id);
                SystemProxyHelper.RefreshOnly();
                throw;
            }
            SystemProxyHelper.RefreshOnly();
            LogBus.Write("Temporary system proxy active; original settings will be restored after the login window.", LogKind.Warn);
        }

        public static RecoveryResult RestoreSystemProxy()
        {
            RecoveryResult result = SystemJournal.Restore(false, null);
            if (result.Restored > 0) SystemProxyHelper.RefreshOnly();
            Report("system_proxy", result);
            return result;
        }

        public static void RecoverExpiredSystemProxy()
        {
            RecoveryResult result = SystemJournal.Restore(true, null);
            if (result.Restored > 0) SystemProxyHelper.RefreshOnly();
            Report("system_proxy", result);
        }

        public static object Status()
        {
            return new { version = 1, environment = StatusJson(EnvironmentJournal.Status()), system_proxy = StatusJson(SystemJournal.Status()),
                process_policy = "manual_restart_only" };
        }

        public static bool HasPending()
        {
            return EnvironmentJournal.Status().Pending || SystemJournal.Status().Pending;
        }

        public static object RestoreAll()
        {
            RecoveryResult env = RestoreEnvironment();
            RecoveryResult system = RestoreSystemProxy();
            return new { version = 1, success = env.Success && system.Success,
                environment = ResultJson(env), system_proxy = ResultJson(system) };
        }

        private static object StatusJson(RecoveryStatus status)
        {
            return new { pending = status.Pending, transaction_id = status.TransactionId,
                expires_utc_ticks = status.ExpiresUtcTicks, values = status.Values };
        }

        private static object ResultJson(RecoveryResult result)
        {
            return new { success = result.Success, code = result.Code, transaction_id = result.TransactionId,
                restored = result.Restored, conflicts = result.Conflicts.ToArray() };
        }

        public static int WatchSystemProxy(string id)
        {
            RecoveryStatus status = SystemJournal.Status();
            if (!status.Pending || status.TransactionId != id) return 0;
            long remainingTicks = status.ExpiresUtcTicks - DateTime.UtcNow.Ticks;
            if (remainingTicks > TimeSpan.FromMinutes(3).Ticks) return 65;
            if (remainingTicks > 0) Thread.Sleep(TimeSpan.FromTicks(remainingTicks));
            RecoveryResult result = SystemJournal.Restore(true, id);
            if (result.Restored > 0) SystemProxyHelper.RefreshOnly();
            Report("system_proxy_watchdog", result);
            return result.Success ? 0 : 3;
        }

        private static void Report(string group, RecoveryResult result)
        {
            if (result.Code == "no_transaction" || result.Code == "not_expired") return;
            // Do not log proxy URLs, user environment values or journal payloads.
            LogBus.Write("Recovery " + group + ": " + result.Code + "; restored=" + result.Restored +
                "; conflicts=" + result.Conflicts.Count, result.Success ? LogKind.Detail : LogKind.Warn);
        }
    }

    internal static class SafeCommand
    {
        public static int RecoveryExitCode()
        {
            return SafeRecovery.HasPending() ? 3 : 0;
        }

        public static bool TryHandle(string[] args, out int code)
        {
            code = 0;
            if (args.Length == 0 || !args[0].StartsWith("--safe-", StringComparison.Ordinal)) return false;
            try
            {
                object result;
                if (args.Length == 1 && args[0] == "--safe-status-json") result = SafeRecovery.Status();
                else if (args.Length == 1 && args[0] == "--safe-recover-json") result = SafeRecovery.RestoreAll();
                else if (args.Length == 1 && args[0] == "--safe-enable-json")
                {
                    Settings settings = Settings.Load();
                    SafeRecovery.RecoverExpiredSystemProxy();
                    EnableOutcome outcome = Operations.Enable(settings);
                    bool success = outcome.ShortcutsDone && (!settings.OptSetEnvVars || outcome.EnvDone) &&
                        (!settings.OptTakeOverShortcut || outcome.TakeOverDone) && (!settings.OptAutoLaunch || outcome.Launched);
                    code = success ? 0 : 3;
                    result = new { version = 1, success = success, code = success ? "enabled" : "partial_failure",
                        environment_applied = outcome.EnvDone, shortcuts_created = outcome.ShortcutsDone,
                        shortcut_taken_over = outcome.TakeOverDone, launched = outcome.Launched,
                        process_policy = "manual_restart_only" };
                }
                else if (args.Length == 1 && args[0] == "--safe-launch-json")
                {
                    SafeRecovery.RecoverExpiredSystemProxy();
                    Operations.LaunchNow(Settings.Load(), false);
                    result = new { version = 1, success = true, code = "launched_or_activated", process_policy = "manual_restart_only" };
                }
                else if (args.Length == 2 && args[0] == "--safe-watch-system")
                { code = SafeRecovery.WatchSystemProxy(args[1]); return true; }
                else
                { code = 64; result = new { version = 1, success = false, code = "invalid_arguments" }; }
                Console.WriteLine(new JavaScriptSerializer().Serialize(result));
                if (args[0] == "--safe-recover-json")
                    code = RecoveryExitCode();
            }
            catch (ManualRestartRequiredException)
            {
                code = 4;
                Console.WriteLine("{\"version\":1,\"success\":false,\"code\":\"manual_restart_required\",\"process_policy\":\"manual_restart_only\"}");
            }
            catch (Exception)
            {
                code = 1;
                Console.WriteLine("{\"version\":1,\"success\":false,\"code\":\"operation_failed\"}");
            }
            return true;
        }
    }

    internal sealed class ManualRestartRequiredException : InvalidOperationException
    {
        public ManualRestartRequiredException() : base("Please close the existing ChatGPT/Codex window manually before relaunching. This build never closes processes by name.") { }
    }
}

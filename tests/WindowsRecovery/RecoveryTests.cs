using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using Microsoft.Win32;
using CodexPersonalProxy;

internal static class RecoveryTests
{
    private static string keyPath;
    private static string tempRoot;
    private static int assertions;

    private static int Main()
    {
        keyPath = @"Software\CodexPersonalProxyTool.Tests\" + Guid.NewGuid().ToString("N");
        tempRoot = Path.Combine(Path.GetTempPath(), "CodexPersonalProxyTool.Tests-" + Guid.NewGuid().ToString("N"));
        try
        {
            Directory.CreateDirectory(tempRoot);
            TestValueKindsAndAbsentValues();
            TestIdempotenceAndCrashReload();
            TestIndependentConflictRecovery();
            TestGroupConflictRecovery();
            TestExpirationAndTransactionIdentity();
            TestEncryptedJournalAndCorruptionFailClosed();
            Console.WriteLine("PASS: " + assertions + " assertions; touched only " + keyPath);
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.ToString());
            return 1;
        }
        finally
        {
            // These random paths are created by this process and never overlap user keys.
            using (RegistryKey parent = Registry.CurrentUser.OpenSubKey(@"Software\CodexPersonalProxyTool.Tests", true))
                if (parent != null) parent.DeleteSubKeyTree(keyPath.Substring(keyPath.LastIndexOf('\\') + 1), false);
            if (Directory.Exists(tempRoot)) Directory.Delete(tempRoot, true);
        }
    }

    private static RegistryJournal Journal(string name, bool group)
    {
        return new RegistryJournal(keyPath, Path.Combine(tempRoot, name + ".bin"), group);
    }

    private static void Check(bool condition, string message)
    {
        assertions++;
        if (!condition) throw new Exception("Assertion failed: " + message);
    }

    private static RegistrySnapshot Read(string name)
    {
        using (RegistryKey key = Registry.CurrentUser.OpenSubKey(keyPath))
            return RegistrySnapshot.Read(key, name);
    }

    private static void Set(string name, object value, RegistryValueKind kind)
    {
        using (RegistryKey key = Registry.CurrentUser.CreateSubKey(keyPath)) key.SetValue(name, value, kind);
    }

    private static void TestValueKindsAndAbsentValues()
    {
        string[] names = { "String", "Expand", "Dword", "Qword", "Binary", "Multi", "Absent" };
        Set("String", "old sentinel string", RegistryValueKind.String);
        Set("Expand", "%CODEX_PROXY_TEST%\\old", RegistryValueKind.ExpandString);
        Set("Dword", unchecked((int)0x87654321), RegistryValueKind.DWord);
        Set("Qword", unchecked((long)0xFEDCBA9876543210), RegistryValueKind.QWord);
        Set("Binary", new byte[] { 0, 1, 127, 255 }, RegistryValueKind.Binary);
        Set("Multi", new[] { "alpha", "beta;gamma" }, RegistryValueKind.MultiString);
        var before = new Dictionary<string, RegistrySnapshot>();
        var desired = new Dictionary<string, RegistrySnapshot>();
        foreach (string name in names)
        {
            before[name] = Read(name);
            desired[name] = RegistrySnapshot.String("managed-" + name);
        }
        RegistryJournal journal = Journal("kinds", false);
        journal.Apply(desired, null);
        RecoveryResult restored = journal.Restore(false, null);
        Check(restored.Success && restored.Restored == names.Length, "restores all registry types and missing values");
        foreach (string name in names) Check(Read(name).Same(before[name]), "preserves original kind/payload for " + name);
    }

    private static void TestIdempotenceAndCrashReload()
    {
        Set("Reload", "before", RegistryValueKind.String);
        var desired = new Dictionary<string, RegistrySnapshot> { { "Reload", RegistrySnapshot.String("after") } };
        RegistryJournal first = Journal("reload", false);
        string id = first.Apply(desired, null);
        Check(first.Apply(desired, null) == id, "same activation is idempotent");
        RegistryJournal afterRestart = Journal("reload", false);
        Check(afterRestart.Status().Pending, "a new journal instance recovers the persisted transaction");
        Set("Reload", "before", RegistryValueKind.String); // Simulate a crash between per-value writes.
        Check(afterRestart.Restore(false, id).Success, "recovery accepts values not yet changed at crash time");
        Check(Read("Reload").Same(RegistrySnapshot.String("before")), "crash recovery restores original value");
    }

    private static void TestIndependentConflictRecovery()
    {
        Set("EnvA", "original-a", RegistryValueKind.String);
        Set("EnvB", "original-b", RegistryValueKind.String);
        var desired = new Dictionary<string, RegistrySnapshot> {
            { "EnvA", RegistrySnapshot.String("managed-a") }, { "EnvB", RegistrySnapshot.String("managed-b") }
        };
        RegistryJournal journal = Journal("independent", false);
        journal.Apply(desired, null);
        Set("EnvB", "external edit", RegistryValueKind.String);
        RecoveryResult result = journal.Restore(false, null);
        Check(!result.Success && result.Conflicts.Contains("EnvB"), "reports an externally modified value");
        Check(Read("EnvA").Same(RegistrySnapshot.String("original-a")), "restores unrelated values despite one conflict");
        Check(Read("EnvB").Same(RegistrySnapshot.String("external edit")), "never overwrites external value");
        Set("EnvB", "managed-b", RegistryValueKind.String);
        Check(Journal("independent", false).Restore(false, null).Success, "remaining conflict can be restored after review");
    }

    private static void TestGroupConflictRecovery()
    {
        Set("ProxyEnable", 0, RegistryValueKind.DWord);
        Set("ProxyServer", "user.proxy:9", RegistryValueKind.String);
        var desired = new Dictionary<string, RegistrySnapshot> {
            { "ProxyEnable", RegistrySnapshot.DWord(1) }, { "ProxyServer", RegistrySnapshot.String("managed.proxy:8") }
        };
        RegistryJournal journal = Journal("group", true);
        journal.Apply(desired, null);
        Set("ProxyServer", "external.proxy:7", RegistryValueKind.String);
        RecoveryResult conflict = journal.Restore(false, null);
        Check(!conflict.Success && conflict.Restored == 0, "system proxy conflict aborts the coupled restore");
        Check(Read("ProxyEnable").Same(RegistrySnapshot.DWord(1)), "group restore leaves all members unchanged on conflict");
        Set("ProxyServer", "managed.proxy:8", RegistryValueKind.String);
        Check(journal.Restore(false, null).Success, "group can be fully restored when current state is ours");
        Check(Read("ProxyEnable").Same(RegistrySnapshot.DWord(0)), "group restore returns enabled flag to original");
        Check(Read("ProxyServer").Same(RegistrySnapshot.String("user.proxy:9")), "group restore returns server to original");
    }

    private static void TestExpirationAndTransactionIdentity()
    {
        var desired = new Dictionary<string, RegistrySnapshot> { { "Window", RegistrySnapshot.String("temporary") } };
        RegistryJournal journal = Journal("expiration", false);
        string id = journal.Apply(desired, DateTime.UtcNow.AddMinutes(10));
        Check(journal.Restore(true, null).Code == "not_expired", "watchdog ignores an unexpired window");
        Check(journal.Restore(false, "stale-id").Code == "different_transaction", "stale watchdog cannot restore a newer window");
        Check(journal.Restore(true, id).Code == "not_expired", "matching id still respects expiration");
        Check(journal.Status().Pending, "non-expired transaction remains pending");
        Check(journal.Restore(false, id).Success, "explicit restore accepts the current transaction id");
    }

    private static void TestEncryptedJournalAndCorruptionFailClosed()
    {
        var desired = new Dictionary<string, RegistrySnapshot> {
            { "Secret", RegistrySnapshot.String("sentinel-must-not-appear-in-journal") }
        };
        RegistryJournal journal = Journal("encrypted", false);
        journal.Apply(desired, null);
        byte[] encrypted = File.ReadAllBytes(Path.Combine(tempRoot, "encrypted.bin"));
        Check(!EncodingContains(encrypted, "sentinel-must-not-appear-in-journal"), "journal never stores plaintext registry values");
        journal.Restore(false, null);

        string badPath = Path.Combine(tempRoot, "corrupt.bin");
        File.WriteAllBytes(badPath, new byte[] { 1, 2, 3, 4, 5 });
        RegistryJournal corrupt = new RegistryJournal(keyPath, badPath, false);
        bool failedClosed = false;
        try { corrupt.Restore(false, null); }
        catch (CryptographicException) { failedClosed = true; }
        Check(failedClosed && Read("Secret").Same(new RegistrySnapshot()), "corrupt DPAPI journal never mutates registry values");
    }

    private static bool EncodingContains(byte[] bytes, string value)
    {
        byte[] needle = System.Text.Encoding.UTF8.GetBytes(value);
        for (int i = 0; i <= bytes.Length - needle.Length; i++)
        {
            bool equal = true;
            for (int j = 0; j < needle.Length; j++) if (bytes[i + j] != needle[j]) { equal = false; break; }
            if (equal) return true;
        }
        return false;
    }
}

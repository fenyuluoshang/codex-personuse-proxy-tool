// Codex Personal Proxy Tool - crash-safe registry transactions, C# 5 / .NET 4.8.
// This source is added to an isolated copy of the MIT licensed proxy manager.
using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Xml.Serialization;
using Microsoft.Win32;

namespace CodexPersonalProxy
{
    public sealed class RegistrySnapshot
    {
        public bool Exists;
        public int Kind;
        public string Text;
        public string[] Strings;
        public byte[] Bytes;
        public long Number;

        public static RegistrySnapshot String(string value)
        {
            return new RegistrySnapshot { Exists = true, Kind = (int)RegistryValueKind.String, Text = value };
        }

        public static RegistrySnapshot DWord(int value)
        {
            return new RegistrySnapshot { Exists = true, Kind = (int)RegistryValueKind.DWord, Number = value };
        }

        public static RegistrySnapshot Read(RegistryKey key, string name)
        {
            if (key == null || !key.GetValueNames().Contains(name, StringComparer.OrdinalIgnoreCase))
                return new RegistrySnapshot();
            var snapshot = new RegistrySnapshot { Exists = true, Kind = (int)key.GetValueKind(name) };
            object value = key.GetValue(name, null, RegistryValueOptions.DoNotExpandEnvironmentNames);
            switch ((RegistryValueKind)snapshot.Kind)
            {
                case RegistryValueKind.String:
                case RegistryValueKind.ExpandString: snapshot.Text = (string)value; break;
                case RegistryValueKind.MultiString: snapshot.Strings = (string[])value; break;
                case RegistryValueKind.Binary:
                case RegistryValueKind.None: snapshot.Bytes = (byte[])value; break;
                case RegistryValueKind.DWord: snapshot.Number = (int)value; break;
                case RegistryValueKind.QWord: snapshot.Number = (long)value; break;
                default: throw new InvalidOperationException("Unsupported registry value kind");
            }
            return snapshot;
        }

        public void Write(RegistryKey key, string name)
        {
            if (!Exists) { key.DeleteValue(name, false); return; }
            object value;
            switch ((RegistryValueKind)Kind)
            {
                case RegistryValueKind.String:
                case RegistryValueKind.ExpandString: value = Text; break;
                case RegistryValueKind.MultiString: value = Strings; break;
                case RegistryValueKind.Binary:
                case RegistryValueKind.None: value = Bytes; break;
                case RegistryValueKind.DWord: value = unchecked((int)Number); break;
                case RegistryValueKind.QWord: value = Number; break;
                default: throw new InvalidOperationException("Unsupported registry value kind");
            }
            key.SetValue(name, value, (RegistryValueKind)Kind);
        }

        public bool Same(RegistrySnapshot other)
        {
            if (other == null || Exists != other.Exists) return false;
            if (!Exists) return true;
            if (Kind != other.Kind || Text != other.Text || Number != other.Number) return false;
            if ((Strings == null) != (other.Strings == null) || (Bytes == null) != (other.Bytes == null)) return false;
            return (Strings == null || Strings.SequenceEqual(other.Strings)) &&
                   (Bytes == null || Bytes.SequenceEqual(other.Bytes));
        }
    }

    public sealed class RegistryChange
    {
        public string Name;
        public RegistrySnapshot Before;
        public RegistrySnapshot After;
    }

    public sealed class JournalDocument
    {
        public int Version = 1;
        public string KeyPath;
        public string TransactionId;
        public long ExpiresUtcTicks;
        public List<RegistryChange> Changes = new List<RegistryChange>();
    }

    public sealed class RecoveryResult
    {
        public string Code = "no_transaction";
        public string TransactionId;
        public int Restored;
        public List<string> Conflicts = new List<string>();
        public bool Success { get { return Conflicts.Count == 0; } }
    }

    public sealed class RecoveryStatus
    {
        public bool Pending;
        public string TransactionId;
        public long ExpiresUtcTicks;
        public int Values;
    }

    /// <summary>
    /// An encrypted write-ahead journal under HKCU. No secret values are logged.
    /// Recovery compares each current value with the value written by this tool.
    /// The comparison protects normal concurrent edits, but Windows registry APIs
    /// cannot provide atomic compare-and-swap against unrelated external writers.
    /// </summary>
    public sealed class RegistryJournal
    {
        private readonly string keyPath;
        private readonly string journalPath;
        private readonly string mutexName;
        private readonly bool restoreAsGroup;
        private static readonly XmlSerializer Serializer = new XmlSerializer(typeof(JournalDocument));

        public RegistryJournal(string keyPath, string journalPath) : this(keyPath, journalPath, false) { }

        public RegistryJournal(string keyPath, string journalPath, bool restoreAsGroup)
        {
            if (String.IsNullOrWhiteSpace(keyPath) || String.IsNullOrWhiteSpace(journalPath))
                throw new ArgumentException("A registry path and journal path are required");
            this.keyPath = keyPath;
            this.journalPath = Path.GetFullPath(journalPath);
            this.restoreAsGroup = restoreAsGroup;
            using (var sha = SHA256.Create())
                mutexName = @"Local\CodexPersonalProxyRecovery-" +
                    BitConverter.ToString(sha.ComputeHash(Encoding.UTF8.GetBytes(this.journalPath.ToLowerInvariant()))).Replace("-", "");
        }

        public string Apply(IDictionary<string, RegistrySnapshot> desired, DateTime? expiresUtc)
        {
            if (desired == null || desired.Count == 0) throw new ArgumentException("No changes requested");
            return Locked(delegate
            {
                JournalDocument document = Load();
                using (RegistryKey key = Registry.CurrentUser.CreateSubKey(keyPath))
                {
                    if (key == null) throw new IOException("Cannot open managed registry key");
                    if (document != null)
                    {
                        // Repeated activation may be idempotent; changed settings require
                        // restoring the pending transaction first to retain its original state.
                        bool same = document.Changes.Count == desired.Count && document.Changes.All(change =>
                            desired.ContainsKey(change.Name) && change.After.Same(desired[change.Name]) &&
                            change.After.Same(RegistrySnapshot.Read(key, change.Name)));
                        if (same) return document.TransactionId;
                        throw new InvalidOperationException("A recovery transaction is pending; restore it before changing settings");
                    }
                    document = new JournalDocument { KeyPath = keyPath, TransactionId = Guid.NewGuid().ToString("N"),
                        ExpiresUtcTicks = expiresUtc.HasValue ? expiresUtc.Value.ToUniversalTime().Ticks : 0 };
                    foreach (var pair in desired)
                        document.Changes.Add(new RegistryChange { Name = pair.Key, Before = RegistrySnapshot.Read(key, pair.Key), After = pair.Value });
                    Save(document); // Durable original + intended state before any mutation.
                    foreach (RegistryChange change in document.Changes) change.After.Write(key, change.Name);
                    key.Flush();
                    return document.TransactionId;
                }
            });
        }

        public RecoveryResult Restore(bool expiredOnly, string transactionId)
        {
            return Locked(delegate
            {
                JournalDocument document = Load();
                var result = new RecoveryResult();
                if (document == null) return result;
                result.TransactionId = document.TransactionId;
                if (transactionId != null && transactionId != document.TransactionId)
                { result.Code = "different_transaction"; return result; }
                if (expiredOnly && (document.ExpiresUtcTicks == 0 || document.ExpiresUtcTicks > DateTime.UtcNow.Ticks))
                { result.Code = "not_expired"; return result; }
                var pending = new List<RegistryChange>();
                using (RegistryKey key = Registry.CurrentUser.CreateSubKey(keyPath))
                {
                    if (key == null) throw new IOException("Cannot open managed registry key");
                    if (restoreAsGroup)
                    {
                        foreach (RegistryChange change in document.Changes)
                        {
                            RegistrySnapshot current = RegistrySnapshot.Read(key, change.Name);
                            if (!current.Same(change.Before) && !current.Same(change.After)) result.Conflicts.Add(change.Name);
                        }
                        if (result.Conflicts.Count > 0) { result.Code = "conflict"; return result; }
                    }
                    foreach (RegistryChange change in document.Changes)
                    {
                        RegistrySnapshot current = RegistrySnapshot.Read(key, change.Name);
                        if (current.Same(change.Before)) continue; // Not yet applied, or already recovered.
                        if (!current.Same(change.After))
                        { pending.Add(change); result.Conflicts.Add(change.Name); continue; }
                        change.Before.Write(key, change.Name);
                        result.Restored++;
                    }
                    key.Flush();
                }
                document.Changes = pending;
                if (pending.Count == 0) File.Delete(journalPath);
                else Save(document);
                result.Code = pending.Count == 0 ? "restored" : "conflict";
                return result;
            });
        }

        public RecoveryStatus Status()
        {
            return Locked(delegate
            {
                JournalDocument document = Load();
                return document == null ? new RecoveryStatus() : new RecoveryStatus {
                    Pending = true, TransactionId = document.TransactionId, ExpiresUtcTicks = document.ExpiresUtcTicks,
                    Values = document.Changes.Count };
            });
        }

        private T Locked<T>(Func<T> action)
        {
            using (var mutex = new Mutex(false, mutexName))
            {
                bool owns = false;
                try
                {
                    try { owns = mutex.WaitOne(TimeSpan.FromSeconds(10)); }
                    catch (AbandonedMutexException) { owns = true; }
                    if (!owns) throw new TimeoutException("Another recovery operation is active");
                    return action();
                }
                finally { if (owns) mutex.ReleaseMutex(); }
            }
        }

        private JournalDocument Load()
        {
            if (!File.Exists(journalPath)) return null;
            byte[] plain = ProtectedData.Unprotect(File.ReadAllBytes(journalPath), null, DataProtectionScope.CurrentUser);
            using (var stream = new MemoryStream(plain))
            {
                var document = (JournalDocument)Serializer.Deserialize(stream);
                if (document.Version != 1 || document.KeyPath != keyPath || String.IsNullOrEmpty(document.TransactionId))
                    throw new InvalidDataException("Recovery journal is incompatible");
                return document;
            }
        }

        private void Save(JournalDocument document)
        {
            byte[] cipher;
            using (var stream = new MemoryStream())
            {
                Serializer.Serialize(stream, document);
                cipher = ProtectedData.Protect(stream.ToArray(), null, DataProtectionScope.CurrentUser);
            }
            Directory.CreateDirectory(Path.GetDirectoryName(journalPath));
            string temp = journalPath + ".new";
            using (var stream = new FileStream(temp, FileMode.Create, FileAccess.Write, FileShare.None))
            { stream.Write(cipher, 0, cipher.Length); stream.Flush(true); }
            if (File.Exists(journalPath)) File.Replace(temp, journalPath, null);
            else File.Move(temp, journalPath);
        }
    }
}

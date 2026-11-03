using System;
using System.Runtime.InteropServices;
using Microsoft.Win32;

namespace ChatGPTProxyManager
{
    public static class SystemProxyHelper
    {
        private const string KeyPath = @"Software\Microsoft\Windows\CurrentVersion\Internet Settings";
        public static void Enable(string host, int port) { OpenWithAutoClose(host, port, 20); }
        public static void Disable() { SafeRecovery.RestoreSystemProxy(); }
        public static void OpenWithAutoClose(string host, int port, int seconds) { SafeRecovery.OpenSystemProxy(host, port, seconds); }
        public static void ResetDisabledFlag(bool ignored) { }
        public static void RefreshOnly()
        {
            IntPtr result;
            Native.SendMessageTimeout(new IntPtr(0xffff), 0x001A, IntPtr.Zero, "Internet Settings", 0x0002, 3000, out result);
            Native.InternetSetOption(IntPtr.Zero, 39, IntPtr.Zero, 0);
            Native.InternetSetOption(IntPtr.Zero, 37, IntPtr.Zero, 0);
        }
        public static bool IsEnabled()
        {
            using (RegistryKey key = Registry.CurrentUser.OpenSubKey(KeyPath))
            {
                object value = key == null ? null : key.GetValue("ProxyEnable");
                return value != null && Convert.ToInt32(value) != 0;
            }
        }
        public static bool IsOurServerProxyOn(string host, int port)
        {
            using (RegistryKey key = Registry.CurrentUser.OpenSubKey(KeyPath))
            {
                object enabled = key == null ? null : key.GetValue("ProxyEnable");
                string server = key == null ? null : key.GetValue("ProxyServer") as string;
                return enabled != null && Convert.ToInt32(enabled) == 1 && String.Equals(server, host + ":" + port, StringComparison.OrdinalIgnoreCase);
            }
        }
    }
}

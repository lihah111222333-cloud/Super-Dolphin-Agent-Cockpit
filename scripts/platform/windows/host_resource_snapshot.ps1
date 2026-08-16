$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;

public static class WindowsHostResourceSnapshot
{
    private const ushort AllProcessorGroups = 0xffff;

    [StructLayout(LayoutKind.Sequential)]
    private struct FileTime
    {
        internal uint Low;
        internal uint High;
    }

    [StructLayout(LayoutKind.Sequential)]
    private sealed class MemoryStatusEx
    {
        internal uint Length = (uint)Marshal.SizeOf(typeof(MemoryStatusEx));
        internal uint MemoryLoad;
        internal ulong TotalPhysical;
        internal ulong AvailablePhysical;
        internal ulong TotalPageFile;
        internal ulong AvailablePageFile;
        internal ulong TotalVirtual;
        internal ulong AvailableVirtual;
        internal ulong AvailableExtendedVirtual;
    }

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GetSystemTimes(out FileTime idle, out FileTime kernel, out FileTime user);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GlobalMemoryStatusEx([In, Out] MemoryStatusEx status);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern uint GetActiveProcessorCount(ushort groupNumber);

    private static ulong ToUInt64(FileTime value)
    {
        return ((ulong)value.High << 32) | value.Low;
    }

    private static Tuple<ulong, ulong> ReadProcessorTimes()
    {
        FileTime idle;
        FileTime kernel;
        FileTime user;
        if (!GetSystemTimes(out idle, out kernel, out user))
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "GetSystemTimes failed");
        }
        return Tuple.Create(ToUInt64(kernel) + ToUInt64(user), ToUInt64(idle));
    }

    public static string Capture()
    {
        Tuple<ulong, ulong> first = ReadProcessorTimes();
        System.Threading.Thread.Sleep(TimeSpan.FromSeconds(5));
        Tuple<ulong, ulong> second = ReadProcessorTimes();
        if (second.Item1 <= first.Item1 || second.Item2 < first.Item2)
        {
            throw new InvalidOperationException("Windows processor counters did not advance monotonically");
        }

        ulong totalDelta = second.Item1 - first.Item1;
        ulong idleDelta = second.Item2 - first.Item2;
        if (idleDelta > totalDelta)
        {
            throw new InvalidOperationException("Windows idle processor time exceeded total processor time");
        }
        double cpuBusyPercent = 100.0 * (totalDelta - idleDelta) / totalDelta;

        MemoryStatusEx memory = new MemoryStatusEx();
        if (!GlobalMemoryStatusEx(memory))
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "GlobalMemoryStatusEx failed");
        }
        if (memory.TotalPhysical == 0 || memory.AvailablePhysical > memory.TotalPhysical)
        {
            throw new InvalidOperationException("Windows physical memory counters are invalid");
        }
        double memoryFreePercent = 100.0 * memory.AvailablePhysical / memory.TotalPhysical;

        uint logicalProcessors = GetActiveProcessorCount(AllProcessorGroups);
        if (logicalProcessors == 0)
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "GetActiveProcessorCount failed");
        }

        return string.Format(
            System.Globalization.CultureInfo.InvariantCulture,
            "{0:F1} {1} {2:F1}",
            cpuBusyPercent,
            logicalProcessors,
            memoryFreePercent);
    }
}
'@

[WindowsHostResourceSnapshot]::Capture()

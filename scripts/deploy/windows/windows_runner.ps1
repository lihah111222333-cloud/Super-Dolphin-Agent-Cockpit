[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$RepoPath,
    [string]$Command,
    [string]$CommandBase64,
    [string]$RunnerId = 'windows-host',
    [int]$TimeoutSeconds = 120
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Stop-Fast([string]$Message) { throw "windows_runner: $Message" }
function Get-NodeExecutorMutexName {
    $mutexName = 'Global\SuperDolphin-Heteroci-NodeExecutor-v1'
    if ($mutexName.Length -gt 260) { Stop-Fast 'node executor mutex name exceeds the Windows limit' }
    return $mutexName
}
if ($env:OS -ne 'Windows_NT') { Stop-Fast 'runner only runs on Windows' }
if (-not [System.IO.Path]::IsPathRooted($RepoPath)) { Stop-Fast '-RepoPath must be absolute' }
if (-not (Test-Path -LiteralPath $RepoPath -PathType Container)) { Stop-Fast "repository does not exist: $RepoPath" }
if ($TimeoutSeconds -le 0 -or $TimeoutSeconds -gt 120) { Stop-Fast 'TimeoutSeconds must be in 1..120' }
if ([string]::IsNullOrWhiteSpace($Command) -eq [string]::IsNullOrWhiteSpace($CommandBase64)) {
    Stop-Fast 'exactly one of -Command or -CommandBase64 is required'
}
if (-not [string]::IsNullOrWhiteSpace($CommandBase64)) {
    try {
        $Command = [Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($CommandBase64))
    }
    catch {
        Stop-Fast 'CommandBase64 is not valid UTF-16LE base64'
    }
}
if ([string]::IsNullOrWhiteSpace($Command)) { Stop-Fast 'decoded command must not be empty or whitespace' }

function ConvertTo-StrictPositiveUInt64 {
    param(
        [Parameter(Mandatory = $true)][object]$Value,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if ($null -eq $Value) { Stop-Fast "$Label is missing" }
    $text = [string]$Value
    if ($text -notmatch '^[1-9][0-9]*$') {
        Stop-Fast "$Label must be a positive integer"
    }
    try {
        $parsed = [uint64]$text
    }
    catch {
        Stop-Fast "$Label is outside the uint64 range"
    }
    if ($parsed -le 0 -or $parsed -gt [uint64][long]::MaxValue) {
        Stop-Fast "$Label is outside the supported positive range"
    }
    return $parsed
}

$computerRecords = @(Get-CimInstance -ClassName Win32_ComputerSystem -Property NumberOfLogicalProcessors,TotalPhysicalMemory -ErrorAction Stop)
if ($computerRecords.Count -ne 1) {
    Stop-Fast 'Win32_ComputerSystem probe must return exactly one record'
}
$computer = $computerRecords[0]
$logicalProcessorsProperty = $computer.PSObject.Properties['NumberOfLogicalProcessors']
$physicalMemoryProperty = $computer.PSObject.Properties['TotalPhysicalMemory']
if ($null -eq $logicalProcessorsProperty -or $null -eq $physicalMemoryProperty) {
    Stop-Fast 'Win32_ComputerSystem probe omitted a required total resource fact'
}
$totalLogicalProcessors = ConvertTo-StrictPositiveUInt64 -Value $logicalProcessorsProperty.Value -Label 'Win32_ComputerSystem.NumberOfLogicalProcessors'
$totalPhysicalMemoryBytes = ConvertTo-StrictPositiveUInt64 -Value $physicalMemoryProperty.Value -Label 'Win32_ComputerSystem.TotalPhysicalMemory'

# Local Windows execution is useful for development probing only.  It cannot
# produce remote CI PASS/evidence, so make the boundary visible to the child
# process and to the single JSON result without adding command-line parameters.
$env:LOCAL_NON_AUTHORITATIVE = '1'
$env:NOT_VERIFIED = '1'
$env:SUPER_DOLPHIN_AUTHORITY = 'LOCAL_NON_AUTHORITATIVE'
$env:SUPER_DOLPHIN_RESULT_STATUS = 'NOT_VERIFIED'
$env:SUPER_DOLPHIN_RUNTIME = 'windows_native'

Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;

public static class SuperDolphinWindows {
  public const uint CREATE_SUSPENDED = 0x00000004;
  const uint CREATE_UNICODE_ENVIRONMENT = 0x00000400;
  const uint CREATE_NO_WINDOW = 0x08000000;
  const uint WAIT_OBJECT_0 = 0x00000000;
  const uint WAIT_TIMEOUT = 0x00000102;
  const uint WAIT_FAILED = 0xffffffff;
  const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000;
  const int JobObjectExtendedLimitInformation = 9;

  [StructLayout(LayoutKind.Sequential)]
  struct JOBOBJECT_BASIC_LIMIT_INFORMATION {
    public long PerProcessUserTimeLimit;
    public long PerJobUserTimeLimit;
    public uint LimitFlags;
    public UIntPtr MinimumWorkingSetSize;
    public UIntPtr MaximumWorkingSetSize;
    public uint ActiveProcessLimit;
    public UIntPtr Affinity;
    public uint PriorityClass;
    public uint SchedulingClass;
  }

  [StructLayout(LayoutKind.Sequential)]
  struct IO_COUNTERS {
    public ulong ReadOperationCount;
    public ulong WriteOperationCount;
    public ulong OtherOperationCount;
    public ulong ReadTransferCount;
    public ulong WriteTransferCount;
    public ulong OtherTransferCount;
  }

  [StructLayout(LayoutKind.Sequential)]
  struct JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
    public JOBOBJECT_BASIC_LIMIT_INFORMATION BasicLimitInformation;
    public IO_COUNTERS IoInfo;
    public UIntPtr ProcessMemoryLimit;
    public UIntPtr JobMemoryLimit;
    public UIntPtr PeakProcessMemoryUsed;
    public UIntPtr PeakJobMemoryUsed;
  }

  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct STARTUPINFO {
    public int cb;
    public IntPtr lpReserved;
    public IntPtr lpDesktop;
    public IntPtr lpTitle;
    public int dwX;
    public int dwY;
    public int dwXSize;
    public int dwYSize;
    public int dwXCountChars;
    public int dwYCountChars;
    public int dwFillAttribute;
    public int dwFlags;
    public short wShowWindow;
    public short cbReserved2;
    public IntPtr lpReserved2;
    public IntPtr hStdInput;
    public IntPtr hStdOutput;
    public IntPtr hStdError;
  }

  [StructLayout(LayoutKind.Sequential)]
  struct PROCESS_INFORMATION {
    public IntPtr hProcess;
    public IntPtr hThread;
    public uint dwProcessId;
    public uint dwThreadId;
  }

  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern IntPtr CreateJobObjectW(IntPtr attributes, string name);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool SetInformationJobObject(
      IntPtr job,
      int infoClass,
      ref JOBOBJECT_EXTENDED_LIMIT_INFORMATION info,
      uint length);

  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern bool CreateProcessW(
      string applicationName,
      StringBuilder commandLine,
      IntPtr processAttributes,
      IntPtr threadAttributes,
      bool inheritHandles,
      uint creationFlags,
      IntPtr environment,
      string currentDirectory,
      ref STARTUPINFO startupInfo,
      out PROCESS_INFORMATION processInformation);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern uint ResumeThread(IntPtr thread);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool TerminateJobObject(IntPtr job, uint exitCode);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool TerminateProcess(IntPtr process, uint exitCode);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool GetExitCodeProcess(IntPtr process, out uint exitCode);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool CloseHandle(IntPtr handle);

  public sealed class Launch {
    public IntPtr ProcessHandle { get; internal set; }
    public IntPtr ThreadHandle { get; internal set; }
    public uint ProcessId { get; internal set; }
  }

  public static IntPtr CreateJob() {
    IntPtr job = CreateJobObjectW(IntPtr.Zero, null);
    if (job == IntPtr.Zero) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateJobObjectW failed");
    }
    var info = new JOBOBJECT_EXTENDED_LIMIT_INFORMATION();
    info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if (!SetInformationJobObject(
        job,
        JobObjectExtendedLimitInformation,
        ref info,
        (uint)Marshal.SizeOf(typeof(JOBOBJECT_EXTENDED_LIMIT_INFORMATION)))) {
      int error = Marshal.GetLastWin32Error();
      bool closed = CloseHandle(job);
      if (!closed) {
        int closeError = Marshal.GetLastWin32Error();
        throw new AggregateException(
            "SetInformationJobObject failed and the Job handle could not be closed",
            new Win32Exception(error, "SetInformationJobObject failed"),
            new Win32Exception(closeError, "CloseHandle(Job Object) failed"));
      }
      throw new Win32Exception(error, "SetInformationJobObject failed");
    }
    return job;
  }

  public static string BuildPowerShellCommandLine(string command) {
    if (command == null || command.Length == 0) {
      throw new ArgumentException("PowerShell command is empty", "command");
    }
    if (command.IndexOf('\0') >= 0) {
      throw new ArgumentException("PowerShell command contains a NUL character", "command");
    }
    return "powershell.exe -NoProfile -Command " + QuoteWindowsArgument(command);
  }

  // Quote one argument using the CommandLineToArgvW escaping rules.  This
  // preserves the existing -Command string semantics while using CreateProcessW.
  static string QuoteWindowsArgument(string argument) {
    var result = new StringBuilder(argument.Length + 2);
    result.Append('"');
    int backslashes = 0;
    foreach (char current in argument) {
      if (current == '\\') {
        backslashes++;
        continue;
      }
      if (current == '"') {
        result.Append('\\', backslashes * 2 + 1);
        result.Append('"');
        backslashes = 0;
        continue;
      }
      result.Append('\\', backslashes);
      result.Append(current);
      backslashes = 0;
    }
    result.Append('\\', backslashes * 2);
    result.Append('"');
    return result.ToString();
  }

  public static Launch CreateSuspended(string commandLine, string currentDirectory) {
    if (String.IsNullOrWhiteSpace(commandLine)) {
      throw new ArgumentException("CreateProcessW command line is empty", "commandLine");
    }
    var startup = new STARTUPINFO();
    startup.cb = Marshal.SizeOf(typeof(STARTUPINFO));
    var mutableCommandLine = new StringBuilder(commandLine);
    PROCESS_INFORMATION processInformation;
    bool created = CreateProcessW(
        "powershell.exe",
        mutableCommandLine,
        IntPtr.Zero,
        IntPtr.Zero,
        false,
        CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT | CREATE_NO_WINDOW,
        IntPtr.Zero,
        currentDirectory,
        ref startup,
        out processInformation);
    if (!created) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateProcessW failed");
    }
    return new Launch {
      ProcessHandle = processInformation.hProcess,
      ThreadHandle = processInformation.hThread,
      ProcessId = processInformation.dwProcessId,
    };
  }

  public static void Assign(IntPtr job, IntPtr process) {
    if (job == IntPtr.Zero || process == IntPtr.Zero) {
      throw new ArgumentException("Job and process handles are required");
    }
    if (!AssignProcessToJobObject(job, process)) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "AssignProcessToJobObject failed");
    }
  }

  public static void Resume(IntPtr thread) {
    if (thread == IntPtr.Zero) {
      throw new ArgumentException("Process thread handle is required");
    }
    uint previousSuspendCount = ResumeThread(thread);
    if (previousSuspendCount == 0xffffffff) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "ResumeThread failed");
    }
    if (previousSuspendCount != 1) {
      throw new InvalidOperationException(
          "ResumeThread returned an unexpected previous suspend count: " + previousSuspendCount);
    }
  }

  public static bool WaitForProcess(IntPtr process, uint milliseconds) {
    return WaitForHandle(process, milliseconds, "process");
  }

  static bool WaitForHandle(IntPtr handle, uint milliseconds, string description) {
    if (handle == IntPtr.Zero) {
      throw new ArgumentException(description + " handle is required");
    }
    uint result = WaitForSingleObject(handle, milliseconds);
    if (result == WAIT_OBJECT_0) {
      return true;
    }
    if (result == WAIT_TIMEOUT) {
      return false;
    }
    if (result == WAIT_FAILED) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "WaitForSingleObject(" + description + ") failed");
    }
    throw new InvalidOperationException("WaitForSingleObject(" + description + ") returned " + result);
  }

  public static uint GetExitCode(IntPtr process) {
    uint exitCode;
    if (!GetExitCodeProcess(process, out exitCode)) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "GetExitCodeProcess failed");
    }
    return exitCode;
  }

  // Terminate through the Job when assignment succeeded.  Assignment failure
  // leaves the suspended root outside the Job, so terminate that exact process
  // handle as well.  Both the root and Job are waited before their handles close.
  public static void TerminateAndWait(IntPtr job, IntPtr process, bool assigned, uint milliseconds) {
    var errors = new List<Exception>();
    bool jobTerminationSucceeded = false;
    if (job != IntPtr.Zero) {
      try {
        if (!TerminateJobObject(job, 1)) {
          throw new Win32Exception(Marshal.GetLastWin32Error(), "TerminateJobObject failed");
        }
        jobTerminationSucceeded = true;
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    // A failed Job termination must not leave the suspended/root process
    // running.  TerminateProcess is also required when assignment failed.
    if (process != IntPtr.Zero && (!assigned || !jobTerminationSucceeded)) {
      try {
        if (!TerminateProcess(process, 1)) {
          int terminateError = Marshal.GetLastWin32Error();
          uint waitResult = WaitForSingleObject(process, 0);
          if (waitResult != WAIT_OBJECT_0) {
            throw new Win32Exception(terminateError, "TerminateProcess failed");
          }
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (process != IntPtr.Zero) {
      try {
        if (!WaitForHandle(process, milliseconds, "process")) {
          errors.Add(new TimeoutException("process did not terminate within the cleanup window"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (job != IntPtr.Zero) {
      try {
        if (!WaitForHandle(job, milliseconds, "Job Object")) {
          errors.Add(new TimeoutException("Job Object did not become empty within the cleanup window"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (errors.Count != 0) {
      throw new AggregateException("process tree termination did not complete", errors);
    }
  }

  public static void CloseLaunch(Launch launch) {
    if (launch == null) {
      return;
    }
    var errors = new List<Exception>();
    IntPtr thread = launch.ThreadHandle;
    if (thread != IntPtr.Zero) {
      try {
        if (CloseHandle(thread)) {
          launch.ThreadHandle = IntPtr.Zero;
        } else {
          errors.Add(new Win32Exception(Marshal.GetLastWin32Error(), "CloseHandle(thread) failed"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    IntPtr process = launch.ProcessHandle;
    if (process != IntPtr.Zero) {
      try {
        if (CloseHandle(process)) {
          launch.ProcessHandle = IntPtr.Zero;
        } else {
          errors.Add(new Win32Exception(Marshal.GetLastWin32Error(), "CloseHandle(process) failed"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (errors.Count != 0) {
      throw new AggregateException("process handle cleanup failed", errors);
    }
  }

  public static IntPtr CloseJob(IntPtr job) {
    if (job == IntPtr.Zero) {
      return IntPtr.Zero;
    }
    if (!CloseHandle(job)) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "CloseHandle(Job Object) failed");
    }
    return IntPtr.Zero;
  }
}
'@

function New-RunnerAggregate([string]$Message, [object[]]$Errors) {
    $exceptions = @(
        foreach ($error in @($Errors)) {
            if ($null -eq $error) { continue }
            if ($error -is [System.Management.Automation.ErrorRecord]) {
                $error.Exception
            }
            elseif ($error -is [System.Exception]) {
                $error
            }
            else {
                [System.Exception]::new([string]$error)
            }
        }
    )
    if ($exceptions.Count -eq 0) {
        return [System.Exception]::new($Message)
    }
    return [System.AggregateException]::new($Message, [System.Exception[]]$exceptions)
}

function Close-Resources([ref]$Job, [object]$Launch) {
    $cleanupErrors = @()
    try {
        if ($null -ne $Launch) { [SuperDolphinWindows]::CloseLaunch($Launch) }
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    try {
        if ($Job.Value -ne [IntPtr]::Zero) {
            $Job.Value = [SuperDolphinWindows]::CloseJob($Job.Value)
        }
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    if ($cleanupErrors.Count -ne 0) {
        throw (New-RunnerAggregate -Message 'windows_runner: handle cleanup failed' -Errors $cleanupErrors)
    }
}

function Stop-And-Reap([ref]$Job, [object]$Launch, [bool]$Assigned) {
    $cleanupErrors = @()
    $processHandle = [IntPtr]::Zero
    if ($null -ne $Launch) { $processHandle = $Launch.ProcessHandle }
    try {
        [SuperDolphinWindows]::TerminateAndWait($Job.Value, $processHandle, $Assigned, 5000)
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    try {
        Close-Resources -Job $Job -Launch $Launch
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    if ($cleanupErrors.Count -ne 0) {
        throw (New-RunnerAggregate -Message 'windows_runner: termination/reap cleanup failed' -Errors $cleanupErrors)
    }
}

$commandLine = [SuperDolphinWindows]::BuildPowerShellCommandLine($Command)
$job = [IntPtr]::Zero
$launch = $null
$assigned = $false
$processExited = $false
$cleanupDone = $false
$processExitCode = $null
$primaryError = $null
$cleanupError = $null
$mutexName = Get-NodeExecutorMutexName
$mutex = $null
$mutexHeld = $false
$mutexWasAbandoned = $false
$mutexReleaseError = $null
$mutexDisposeError = $null
$mutexCleanupError = $null

try {
    try {
        $mutex = [System.Threading.Mutex]::new($false, $mutexName)
    }
    catch {
        Stop-Fast 'unable to create node executor mutex'
    }

    try {
        $mutexHeld = [bool]$mutex.WaitOne(0)
    }
    catch [System.Threading.AbandonedMutexException] {
        $mutexHeld = $true
        $mutexWasAbandoned = $true
        Write-Verbose 'node executor mutex was abandoned; continuing with the acquired lock'
    }
    catch {
        Stop-Fast 'unable to acquire node executor mutex'
    }
    if (-not $mutexHeld) { Stop-Fast 'another heteroci node executor is already running' }

    # The Job exists before CreateProcessW so no user code can run outside it.
    $job = [SuperDolphinWindows]::CreateJob()
    $launch = [SuperDolphinWindows]::CreateSuspended($commandLine, $RepoPath)
    [SuperDolphinWindows]::Assign($job, $launch.ProcessHandle)
    $assigned = $true
    [SuperDolphinWindows]::Resume($launch.ThreadHandle)
    if (-not [SuperDolphinWindows]::WaitForProcess($launch.ProcessHandle, [uint32]($TimeoutSeconds * 1000))) {
        Stop-Fast "command exceeded ${TimeoutSeconds}s watchdog"
    }
    $processExited = $true
    $processExitCode = [SuperDolphinWindows]::GetExitCode($launch.ProcessHandle)
}
catch {
    $primaryError = $_.Exception
}
finally {
    if (-not $cleanupDone) {
        try {
            if ($processExited) {
                Close-Resources -Job ([ref]$job) -Launch $launch
            }
            else {
                Stop-And-Reap -Job ([ref]$job) -Launch $launch -Assigned $assigned
            }
            $cleanupDone = $true
        }
        catch {
            $firstCleanupError = $_.Exception
            try {
                if ($processExited) {
                    Close-Resources -Job ([ref]$job) -Launch $launch
                }
                else {
                    Stop-And-Reap -Job ([ref]$job) -Launch $launch -Assigned $assigned
                }
                $cleanupDone = $true
                $cleanupError = $firstCleanupError
            }
            catch {
                $cleanupError = New-RunnerAggregate -Message 'windows_runner: cleanup retry failed' -Errors @($firstCleanupError, $_.Exception)
            }
        }
    }
    if ($null -ne $mutex -and $mutexHeld) {
        try {
            $mutex.ReleaseMutex()
        }
        catch {
            $mutexReleaseError = $_.Exception
        }
    }
    if ($null -ne $mutex) {
        try {
            $mutex.Dispose()
        }
        catch {
            $mutexDisposeError = $_.Exception
        }
    }
    $mutexCleanupExceptions = @()
    if ($null -ne $mutexReleaseError) { $mutexCleanupExceptions += $mutexReleaseError }
    if ($null -ne $mutexDisposeError) { $mutexCleanupExceptions += $mutexDisposeError }
    if ($mutexCleanupExceptions.Count -ne 0) {
        $mutexCleanupError = New-RunnerAggregate -Message 'windows_runner: node executor mutex cleanup failed' -Errors $mutexCleanupExceptions
    }
}

if ($null -ne $primaryError) {
    $failureExceptions = @($primaryError)
    if ($null -ne $cleanupError) { $failureExceptions += $cleanupError }
    if ($null -ne $mutexCleanupError) { $failureExceptions += $mutexCleanupError }
    if ($failureExceptions.Count -gt 1) {
        throw (New-RunnerAggregate -Message 'windows_runner: primary operation failed and cleanup failed' -Errors $failureExceptions)
    }
    throw $primaryError
}
if ($null -ne $processExitCode -and $processExitCode -ne 0) {
    $childExitError = [System.Exception]::new("windows_runner: child exited with code $processExitCode")
    $postExitExceptions = @($childExitError)
    if ($null -ne $cleanupError) { $postExitExceptions += $cleanupError }
    if ($null -ne $mutexCleanupError) { $postExitExceptions += $mutexCleanupError }
    if ($postExitExceptions.Count -gt 1) {
        throw (New-RunnerAggregate -Message 'windows_runner: child exited and cleanup failed' -Errors $postExitExceptions)
    }
    exit $processExitCode
}
if ($null -ne $cleanupError -and $null -ne $mutexCleanupError) {
    throw (New-RunnerAggregate -Message 'windows_runner: process and mutex cleanup failed' -Errors @($cleanupError, $mutexCleanupError))
}
if ($null -ne $cleanupError) {
    throw $cleanupError
}
if ($null -ne $mutexCleanupError) {
    throw $mutexCleanupError
}
[pscustomobject]@{
	runner_id = $RunnerId
	authority = 'LOCAL_NON_AUTHORITATIVE'
	runtime = 'windows_native'
	total_logical_processors = $totalLogicalProcessors
    total_physical_memory_bytes = $totalPhysicalMemoryBytes
    status = 'NOT_VERIFIED'
    local_outcome = 'LOCAL_EXECUTED'
} | ConvertTo-Json -Compress

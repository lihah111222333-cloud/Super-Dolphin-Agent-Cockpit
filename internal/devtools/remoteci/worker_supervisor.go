package remoteci

const remoteWorkerSupervisorPython = "/usr/bin/python3"

const (
	remoteWorkerCommandElementLimit = 256
	remoteWorkerSupervisorChunkSize = 240
	remoteWorkerSupervisorLoader    = `import sys;i=sys.argv.index("--");c="".join(sys.argv[1:i]);sys.argv=sys.argv[i:];exec(c)`
)

// remoteWorkerSupervisorScript 让 ECI 的 PID 1 转发终止信号并回收 worker 的孤儿后代。
const remoteWorkerSupervisorScript = `
import os
import signal
import sys
import time

worker = os.fork()
if worker == 0:
    os.setsid()
    os.execv(sys.argv[1], sys.argv[1:])

def forward(signum, _frame):
    try:
        os.killpg(worker, signum)
    except ProcessLookupError:
        pass

for forwarded_signal in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
    signal.signal(forwarded_signal, forward)

worker_status = None
while worker_status is None:
    try:
        child, status = os.wait()
    except InterruptedError:
        continue
    if child == worker:
        worker_status = status

try:
    os.killpg(worker, signal.SIGTERM)
except ProcessLookupError:
    pass

deadline = time.monotonic() + 2.0
while time.monotonic() < deadline:
    try:
        child, _ = os.waitpid(-1, os.WNOHANG)
    except ChildProcessError:
        break
    if child == 0:
        time.sleep(0.01)

try:
    os.killpg(worker, signal.SIGKILL)
except ProcessLookupError:
    pass

if os.WIFEXITED(worker_status):
    raise SystemExit(os.WEXITSTATUS(worker_status))
raise SystemExit(128 + os.WTERMSIG(worker_status))
`

// remoteWorkerSupervisorCommand 将 PID 1 脚本切成符合 ECI 单项长度限制的命令参数。
func remoteWorkerSupervisorCommand(workerBinary string) []string {
	command := []string{
		remoteWorkerSupervisorPython,
		"-c",
		remoteWorkerSupervisorLoader,
	}
	for remaining := remoteWorkerSupervisorScript; remaining != ""; {
		size := min(len(remaining), remoteWorkerSupervisorChunkSize)
		command = append(command, remaining[:size])
		remaining = remaining[size:]
	}
	return append(command, "--", workerBinary)
}

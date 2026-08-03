package eci

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeCommandRunner struct {
	calls     [][]string
	responses [][]byte
	runErrors []error
	err       error
}

func containsArgumentPair(values []string, key string, value string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == key && values[index+1] == value {
			return true
		}
	}
	return false
}

type blockingCommandRunner struct {
	calls int
}

func (runner *blockingCommandRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	runner.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeCommandRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(f.runErrors) > 0 {
		runErr := f.runErrors[0]
		f.runErrors = f.runErrors[1:]
		if runErr != nil {
			return nil, runErr
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

func TestClient_ECIOperations(t *testing.T) {
	const acceptedImageCacheID = "imc-audit-only-1"
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ContainerGroupId":"eci-created"}`),
		[]byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-created","ContainerGroupName":"shard-1","Status":"Running"}]}`),
		[]byte(`{"Content":"worker output"}`),
		[]byte(`{"RequestId":"request-deleted"}`),
	}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	request.ImageCacheSnapshotID = "snap-runtime-1"
	request.Environment = map[string]string{"Z_LAST": "z-value", "A_FIRST": "a-value"}
	request.Tags = map[string]string{"z-last": "z-value", "a-first": "a-value"}
	created, err := client.CreateContainerGroup(context.Background(), request)
	if err != nil || created.ID != "eci-created" {
		t.Fatalf("CreateContainerGroup() = %#v, %v", created, err)
	}
	groups, err := client.DescribeContainerGroups(context.Background(), "eci-created")
	if err != nil || len(groups) != 1 || groups[0].Status != "Running" {
		t.Fatalf("DescribeContainerGroups() = %#v, %v", groups, err)
	}
	log, err := client.DescribeContainerLog(context.Background(), "eci-created", "worker")
	if err != nil || log != "worker output" {
		t.Fatalf("DescribeContainerLog() = %q, %v", log, err)
	}
	if err := client.DeleteContainerGroup(context.Background(), "eci-created"); err != nil {
		t.Fatalf("DeleteContainerGroup() error = %v", err)
	}
	for _, legacy := range []string{"--DataCacheBucket", "HostPathVolume", "base-data"} {
		if slices.Contains(runner.calls[0], legacy) {
			t.Fatalf("CreateContainerGroup encoded legacy DataCache input %q: %#v", legacy, runner.calls[0])
		}
	}
	for _, pair := range [][]string{
		{"--ImageSnapshotId", request.ImageCacheSnapshotID},
		{"--Container.1.Image", request.MainImage},
		{"--InitContainer.1.Image", request.InitImage},
	} {
		if !containsArgumentPair(runner.calls[0], pair[0], pair[1]) {
			t.Fatalf("CreateContainerGroup call missing explicit image %v: %#v", pair, runner.calls[0])
		}
	}
	if slices.Contains(runner.calls[0], acceptedImageCacheID) {
		t.Fatalf("CreateContainerGroup passed ImageCacheID to the CLI: %#v", runner.calls[0])
	}
}

func TestClientCreateContainerGroupDoesNotEncodeExpandedDataMounts(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	if _, err := client.CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	call := runner.calls[0]
	for _, value := range call {
		if value == "expanded-data" || value == "/opt/super-dolphin-gate" || value == "/usr/bin/xkbcomp" || value == "/usr/share/X11/xkb" {
			t.Fatalf("CreateContainerGroup encoded retired ImageCache overlay value %q: %#v", value, call)
		}
	}
}

func TestClientCreateContainerGroupEncodesOCIMaterializerTempVolume(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	request.InitVolumeMounts = append(request.InitVolumeMounts,
		VolumeMount{Name: "temp-data", MountPath: "/tmp"},
	)
	if _, err := client.CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	call := runner.calls[0]
	for _, pair := range [][]string{
		{"--Volume.3.Name", "temp-data"},
		{"--InitContainer.1.VolumeMount.3.Name", "temp-data"},
		{"--InitContainer.1.VolumeMount.3.MountPath", "/tmp"},
		{"--InitContainer.1.VolumeMount.3.ReadOnly", "false"},
	} {
		if !containsArgumentPair(call, pair[0], pair[1]) {
			t.Fatalf("call missing %v: %#v", pair, call)
		}
	}
}

// TestClientCreateContainerGroupAcceptsECIImageCacheIdentity 覆盖 ECI ImageCache 返回的不可变镜像身份。
func TestClientCreateContainerGroupAcceptsECIImageCacheIdentity(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	request := validCreateRequest()
	image := "ac2-registry.cn-hangzhou.cr.aliyuncs.com/ac2/base@sha256:" + strings.Repeat("a", 64)
	request.MainImage, request.InitImage = image, image
	if _, err := newTestClient(t, runner).CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() rejected ECI ImageCache identity: %v", err)
	}
}

func TestClientCreateContainerGroupEncodesCurrentGateOSSVolume(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	request.BootstrapVolume = OSSVolume{
		Bucket: "super-dolphin-ci", Endpoint: "oss-cn-hangzhou-internal.aliyuncs.com",
		Path: "/baseline-artifacts/31/output", RoleName: "worker-role",
	}
	request.InitVolumeMounts = append(request.InitVolumeMounts,
		VolumeMount{Name: "temp-data", MountPath: "/tmp"},
		VolumeMount{Name: "current-gate", MountPath: "/current-gate", ReadOnly: true},
	)
	if _, err := client.CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	options, err := ossVolumeOptions(request.BootstrapVolume)
	if err != nil {
		t.Fatalf("ossVolumeOptions() error = %v", err)
	}
	call := runner.calls[0]
	for _, pair := range [][]string{
		{"--Volume.4.Name", "current-gate"},
		{"--Volume.4.Type", "FlexVolume"},
		{"--Volume.4.FlexVolume.Driver", "alicloud/oss"},
		{"--Volume.4.FlexVolume.Options", string(options)},
		{"--InitContainer.1.VolumeMount.3.Name", "temp-data"},
		{"--InitContainer.1.VolumeMount.4.Name", "current-gate"},
		{"--InitContainer.1.VolumeMount.4.ReadOnly", "true"},
	} {
		if !containsArgumentPair(call, pair[0], pair[1]) {
			t.Fatalf("call missing %v: %#v", pair, call)
		}
	}
}

func TestClient_DescribeContainerGroupsDecodesTerminalDiagnostics(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-created","ContainerGroupName":"shard-1","Status":"Failed","CreationTime":"2026-07-27T07:59:00Z","FailedTime":"2026-07-27T08:00:00Z","Containers":[{"Name":"worker","CurrentState":{"State":"Terminated","StartTime":"2026-07-27T07:59:10Z","FinishTime":"2026-07-27T08:00:00Z","ExitCode":137,"Reason":"OOMKilled","Message":"memory limit exceeded"}}],"InitContainers":[{"Name":"materializer","CurrentState":{"State":"Terminated","StartTime":"2026-07-27T07:59:01Z","FinishTime":"2026-07-27T07:59:09Z"}}],"Events":[{"Type":"Warning","Reason":"BackOff","Message":"worker exited","Count":2,"LastTimestamp":"2026-07-27T08:00:00Z"}]}]}`),
	}}
	client := newTestClient(t, runner)
	groups, err := client.DescribeContainerGroups(context.Background(), "eci-created")
	if err != nil {
		t.Fatalf("DescribeContainerGroups() error = %v", err)
	}
	exitCode := int64(137)
	want := []ContainerGroup{{
		ID:           "eci-created",
		Name:         "shard-1",
		Status:       "Failed",
		CreationTime: time.Date(2026, 7, 27, 7, 59, 0, 0, time.UTC),
		FailedTime:   time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
		Containers: []ContainerStatus{{
			Name: "worker",
			CurrentState: ContainerState{
				State:      "Terminated",
				StartTime:  time.Date(2026, 7, 27, 7, 59, 10, 0, time.UTC),
				FinishTime: time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
				ExitCode:   &exitCode,
				Reason:     "OOMKilled",
				Message:    "memory limit exceeded",
			},
		}},
		InitContainers: []ContainerStatus{{Name: "materializer", CurrentState: ContainerState{State: "Terminated", StartTime: time.Date(2026, 7, 27, 7, 59, 1, 0, time.UTC), FinishTime: time.Date(2026, 7, 27, 7, 59, 9, 0, time.UTC)}}},
		Events: []ContainerGroupEvent{{
			Type:          "Warning",
			Reason:        "BackOff",
			Message:       "worker exited",
			Count:         2,
			LastTimestamp: "2026-07-27T08:00:00Z",
		}},
	}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("DescribeContainerGroups() = %#v, want %#v", groups, want)
	}
}

func TestClientDescribeContainerLogRejectsResponseBeyondRequestedLimit(t *testing.T) {
	response := []byte(`{"Content":"` + strings.Repeat("x", maxContainerLogBytes+1) + `"}`)
	client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{response}})
	if _, err := client.DescribeContainerLog(context.Background(), "eci-1", "worker"); err == nil ||
		!strings.Contains(err.Error(), "exceeds requested byte limit") {
		t.Fatalf("DescribeContainerLog() error = %v, want response byte limit rejection", err)
	}
}

func TestClient_FailsFastOnCommandAndJSONErrors(t *testing.T) {
	commandCalls := []struct {
		name string
		call func(*Client) error
	}{
		{"create", func(client *Client) error {
			_, err := client.CreateContainerGroup(context.Background(), validCreateRequest())
			return err
		}},
		{"describe", func(client *Client) error {
			_, err := client.DescribeContainerGroups(context.Background(), "eci-1")
			return err
		}},
		{"log", func(client *Client) error {
			_, err := client.DescribeContainerLog(context.Background(), "eci-1", "worker")
			return err
		}},
		{"delete", func(client *Client) error { return client.DeleteContainerGroup(context.Background(), "eci-1") }},
	}
	for _, testCase := range commandCalls {
		t.Run(testCase.name+" command error", func(t *testing.T) {
			client := newTestClient(t, &fakeCommandRunner{err: errors.New("profile unavailable")})
			if err := testCase.call(client); err == nil {
				t.Fatal("operation error = nil")
			}
		})
	}
	t.Run("malformed JSON", func(t *testing.T) {
		client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{[]byte(`not-json`)}})
		if _, err := client.DescribeContainerGroups(context.Background(), "eci-1"); err == nil {
			t.Fatal("DescribeContainerGroups() error = nil")
		}
	})
	t.Run("missing required response field", func(t *testing.T) {
		client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{[]byte(`{}`)}})
		if err := client.DeleteContainerGroup(context.Background(), "eci-1"); err == nil {
			t.Fatal("DeleteContainerGroup() error = nil")
		}
	})
}

func TestClient_RetriesTransientCLIErrors(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{"TLS handshake timeout", errors.New("net/http: TLS handshake timeout")},
		{"I/O timeout", errors.New("read tcp: i/o timeout")},
		{"unexpected EOF", errors.New("unexpected EOF")},
		{"STS EOF", errors.New("Post https://sts.example.invalid: EOF")},
		{"client timeout", errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")},
		{"STS AssumeRole header timeout", errors.New("init client failed Post \"https://sts.example.invalid?AccessKeyId=test\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)")},
		{"STS user flow control", errors.New(`init client failed refresh session token failed: {"Message":"Request was denied due to user flow control.","Code":"Throttling.User"}`)},
		{"connection reset", errors.New("read tcp: connection reset by peer")},
		{"temporary DNS text", errors.New("lookup eci.example.invalid: temporary failure in name resolution")},
		{"temporary DNS error", &net.DNSError{Err: "temporary resolver failure", Name: "eci.example.invalid", IsTemporary: true}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &fakeCommandRunner{
				responses: [][]byte{[]byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-1","ContainerGroupName":"shard-1","Status":"Running"}]}`)},
				runErrors: []error{testCase.err},
			}
			client := newTestClient(t, runner)
			var waits []time.Duration
			client.wait = func(ctx context.Context, delay time.Duration) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				waits = append(waits, delay)
				return nil
			}
			groups, err := client.DescribeContainerGroups(context.Background(), "eci-1")
			if err != nil || len(groups) != 1 || groups[0].Status != "Running" {
				t.Fatalf("DescribeContainerGroups() = %#v, %v", groups, err)
			}
			if len(runner.calls) != 2 || !reflect.DeepEqual(waits, []time.Duration{initialCLIRetryDelay}) {
				t.Fatalf("calls = %d, waits = %v, want 2 calls and one initial wait", len(runner.calls), waits)
			}
		})
	}
}

func TestClient_BoundsEveryTransientCLIAttempt(t *testing.T) {
	runner := &blockingCommandRunner{}
	client := newTestClient(t, runner)
	client.attemptTimeout = time.Millisecond
	client.wait = func(context.Context, time.Duration) error { return nil }

	_, err := client.DescribeContainerGroups(context.Background(), "eci-1")
	if err == nil || runner.calls != maxCLIAttempts {
		t.Fatalf("DescribeContainerGroups() calls=%d error=%v", runner.calls, err)
	}
}

func TestPreserveCommandContextErrorMakesAttemptTimeoutRetryable(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := preserveCommandContextError(ctx, errors.New("signal: killed"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("preserved error = %v, want deadline cause", err)
	}
	if !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("preserved error = %v, want process exit evidence", err)
	}
	if !isTransientCLIError(err) {
		t.Fatalf("attempt timeout = %v, want transient retry classification", err)
	}
	if isTransientCLIError(errors.New("signal: killed")) {
		t.Fatal("unbound signal kill must not be retried")
	}
}

func TestMaxControlPlaneRetryDurationCoversAttemptsAndBackoff(t *testing.T) {
	want := time.Duration(maxCLIAttempts) * cliAttemptTimeout
	for attempt := 1; attempt < maxCLIAttempts; attempt++ {
		want += cliRetryDelay(attempt)
	}
	if got := MaxControlPlaneRetryDuration(); got != want {
		t.Fatalf("MaxControlPlaneRetryDuration() = %v, want %v", got, want)
	}
}

func TestClient_CreateRetryReusesClientToken(t *testing.T) {
	runner := &fakeCommandRunner{
		responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)},
		runErrors: repeatCommandErrors(errors.New("net/http: TLS handshake timeout"), maxCLIAttempts-1),
	}
	client := newTestClient(t, runner)
	client.wait = func(context.Context, time.Duration) error { return nil }
	created, err := client.CreateContainerGroup(context.Background(), validCreateRequest())
	if err != nil || created.ID != "eci-created" {
		t.Fatalf("CreateContainerGroup() = %#v, %v", created, err)
	}
	if len(runner.calls) != maxCLIAttempts {
		t.Fatalf("calls = %d, want %d", len(runner.calls), maxCLIAttempts)
	}
	for _, call := range runner.calls {
		if !reflect.DeepEqual(call, runner.calls[0]) || !containsArgumentPair(call, "--ClientToken", testClientToken) {
			t.Fatalf("Create retries must reuse one ClientToken: %#v", runner.calls)
		}
	}
}

func TestClient_CreateReturnsSpotCapacityErrorWithoutPayAsYouGoFallback(t *testing.T) {
	runner := &fakeCommandRunner{err: errors.New("Code: Spot.NotMatched, Message: no matching spot capacity")}
	client := newTestClient(t, runner)

	if _, err := client.CreateContainerGroup(context.Background(), validCreateRequest()); err == nil {
		t.Fatal("CreateContainerGroup() error = nil")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want one explicit spot request", len(runner.calls))
	}
	if !containsArgumentPair(runner.calls[0], "--SpotStrategy", SpotStrategyAsPriceGo) ||
		containsArgumentPair(runner.calls[0], "--SpotStrategy", SpotStrategyNoSpot) {
		t.Fatalf("spot request = %#v", runner.calls[0])
	}
}

func TestClient_CreateDoesNotFallBackForNonSpotCapacityErrors(t *testing.T) {
	for _, runErr := range []error{
		errors.New("Code: OperationDenied.NoStock, Message: zone inventory unavailable"),
		errors.New("Code: QuotaExceeded, Message: quota exhausted"),
		errors.New("Code: AccessDenied, Message: denied"),
		errors.New("net/http: TLS handshake timeout"),
	} {
		t.Run(runErr.Error(), func(t *testing.T) {
			runner := &fakeCommandRunner{err: runErr}
			client := newTestClient(t, runner)
			if _, err := client.CreateContainerGroup(context.Background(), validCreateRequest()); err == nil {
				t.Fatal("CreateContainerGroup() error = nil")
			}
			for _, call := range runner.calls {
				if containsArgumentPair(call, "--SpotStrategy", "NoSpot") {
					t.Fatalf("unexpected pay-as-you-go fallback: %#v", runner.calls)
				}
			}
		})
	}
}

func TestClient_CreateReconcilesAfterUncertainResponse(t *testing.T) {
	transient := errors.New("net/http: TLS handshake timeout")
	runner := &fakeCommandRunner{
		responses: [][]byte{[]byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-recovered","ContainerGroupName":"shard-1","Status":"Running"}]}`)},
		runErrors: repeatCommandErrors(transient, maxCLIAttempts),
	}
	client := newTestClient(t, runner)
	group, err := client.CreateContainerGroup(context.Background(), validCreateRequest())
	if err != nil || group.ID != "eci-recovered" {
		t.Fatalf("CreateContainerGroup() = %#v, %v", group, err)
	}
	if len(runner.calls) != maxCLIAttempts+1 {
		t.Fatalf("calls = %d, want %d", len(runner.calls), maxCLIAttempts+1)
	}
	reconcileCall := runner.calls[len(runner.calls)-1]
	for _, pair := range [][]string{{"--ContainerGroupName", "shard-1"}, {"--Tag.1.Key", "workload"}, {"--Tag.1.Value", "test"}} {
		if !containsArgumentPair(reconcileCall, pair[0], pair[1]) {
			t.Fatalf("reconcile call missing %v: %#v", pair, reconcileCall)
		}
	}
}

func repeatCommandErrors(err error, count int) []error {
	errors := make([]error, count)
	for index := range errors {
		errors[index] = err
	}
	return errors
}

func TestClient_CreateReconcilesMalformedSuccessResponse(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`not-json`),
		[]byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-recovered","ContainerGroupName":"shard-1","Status":"Pending"}]}`),
	}}
	client := newTestClient(t, runner)
	group, err := client.CreateContainerGroup(context.Background(), validCreateRequest())
	if err != nil || group.ID != "eci-recovered" || len(runner.calls) != 2 {
		t.Fatalf("CreateContainerGroup() = %#v, %v, calls = %d", group, err, len(runner.calls))
	}
}

func TestClient_DoesNotRetryAPIErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{"AccessDenied", errors.New("Code: AccessDenied, Message: fake policy denies this request")},
		{"InvalidParameter", errors.New("Code: InvalidParameter, Message: fake input is invalid")},
		{"business state", errors.New("Code: OperationDenied.NoStock, Message: zone inventory unavailable")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &fakeCommandRunner{err: testCase.err}
			client := newTestClient(t, runner)
			waitCalls := 0
			client.wait = func(context.Context, time.Duration) error {
				waitCalls++
				return nil
			}
			if _, err := client.DescribeContainerGroups(context.Background(), "eci-1"); err == nil {
				t.Fatal("DescribeContainerGroups() error = nil")
			}
			if len(runner.calls) != 1 || waitCalls != 0 {
				t.Fatalf("calls = %d, waits = %d, want one call and no wait", len(runner.calls), waitCalls)
			}
		})
	}
}

func TestClient_StopsRetryWhenContextIsCanceled(t *testing.T) {
	runner := &fakeCommandRunner{err: errors.New("net/http: TLS handshake timeout")}
	client := newTestClient(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.wait = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := client.DescribeContainerGroups(ctx, "eci-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DescribeContainerGroups() error = %v, want context.Canceled", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
}

func TestWaitForRetry_ReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForRetry() error = %v, want context.Canceled", err)
	}
}

func TestClient_StopsAfterBoundedTransientRetries(t *testing.T) {
	runner := &fakeCommandRunner{err: errors.New("net/http: TLS handshake timeout")}
	client := newTestClient(t, runner)
	var waits []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	if _, err := client.DescribeContainerGroups(context.Background(), "eci-1"); err == nil {
		t.Fatal("DescribeContainerGroups() error = nil")
	}
	wantWaits := []time.Duration{
		initialCLIRetryDelay,
		2 * initialCLIRetryDelay,
		4 * initialCLIRetryDelay,
		8 * initialCLIRetryDelay,
		maxCLIRetryDelay,
		maxCLIRetryDelay,
		maxCLIRetryDelay,
		maxCLIRetryDelay,
		maxCLIRetryDelay,
		maxCLIRetryDelay,
		maxCLIRetryDelay,
	}
	if len(runner.calls) != maxCLIAttempts || !reflect.DeepEqual(waits, wantWaits) {
		t.Fatalf("calls = %d, waits = %v, want %d calls and %v", len(runner.calls), waits, maxCLIAttempts, wantWaits)
	}
}

func TestClient_RedactsSensitiveCLIQueryValues(t *testing.T) {
	sensitiveValues := []string{
		"example-access-key-id-not-a-credential",
		"example-access-key-secret-not-a-credential",
		"example-signature-not-a-credential%2Fvalue",
		"example-security-token-not-a-credential",
	}
	requestURL := "https://eci.example.invalid/?AccessKeyId=" + sensitiveValues[0] +
		"&AccessKeySecret=" + sensitiveValues[1] +
		"&Signature=" + sensitiveValues[2] +
		"&SecurityToken=" + sensitiveValues[3]
	runner := &fakeCommandRunner{err: errors.New("Code: AccessDenied, Request URL: " + requestURL)}
	client := newTestClient(t, runner)
	_, err := client.DescribeContainerGroups(context.Background(), "eci-1")
	if err == nil {
		t.Fatal("DescribeContainerGroups() error = nil")
	}
	for _, value := range sensitiveValues {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("DescribeContainerGroups() error leaked fake sensitive value: %v", err)
		}
	}
	for _, parameter := range []string{"AccessKeyId", "AccessKeySecret", "Signature", "SecurityToken"} {
		if !strings.Contains(err.Error(), parameter+"=<redacted>") {
			t.Fatalf("DescribeContainerGroups() error = %v, want %s redacted", err, parameter)
		}
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
}

func TestNewWithRunner_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing profile", func(config *Config) { config.Profile = "" }},
		{"unsupported strategy", func(config *Config) { config.SpotStrategy = "unknown" }},
		{"wrong spot duration", func(config *Config) { config.SpotDurationHours = 0 }},
		{"pay-as-you-go spot duration", func(config *Config) { config.SpotStrategy, config.SpotDurationHours = SpotStrategyNoSpot, 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig()
			test.mutate(&config)
			if _, err := NewWithRunner(config, &fakeCommandRunner{}); err == nil {
				t.Fatal("NewWithRunner() error = nil")
			}
		})
	}
}

func TestNewWithRunner_AcceptsExplicitPayAsYouGo(t *testing.T) {
	config := testConfig()
	config.SpotStrategy = SpotStrategyNoSpot
	config.SpotDurationHours = 0
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-payg"}`)}}
	client, err := NewWithRunner(config, runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	if _, err := client.CreateContainerGroup(context.Background(), validCreateRequest()); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	if len(runner.calls) != 1 || !containsArgumentPair(runner.calls[0], "--SpotStrategy", SpotStrategyNoSpot) ||
		containsArgumentPair(runner.calls[0], "--SpotDuration", "1") {
		t.Fatalf("pay-as-you-go request = %#v", runner.calls)
	}
}

func TestConfig_FieldRegistry(t *testing.T) {
	assertStructFields(t, reflect.TypeFor[Config](), []string{
		"Binary", "RegionID", "VSwitchID", "SecurityGroupID", "WorkerRoleName", "Profile", "Deadline", "SpotStrategy", "SpotDurationHours",
	})
}

func TestCreateRequest_FieldRegistry(t *testing.T) {
	assertStructFields(t, reflect.TypeFor[CreateRequest](), []string{
		"ContainerGroupName", "ContainerName", "ImageCacheSnapshotID", "MainImage", "InitImage", "Resources", "Command", "Args", "Environment", "Tags",
		"InitContainer", "BootstrapVolume", "SourceVolume", "WorkVolume", "TempVolume",
		"MainVolumeMounts", "InitVolumeMounts",
	})
	assertStructFields(t, reflect.TypeFor[Resources](), []string{"CPU", "MemoryGiB"})
	assertStructFields(t, reflect.TypeFor[InitContainer](), []string{"Name", "Command", "Args", "Environment"})
	assertStructFields(t, reflect.TypeFor[EmptyDirVolume](), []string{"Name"})
	assertStructFields(t, reflect.TypeFor[VolumeMount](), []string{"Name", "MountPath", "SubPath", "ReadOnly"})
}

func TestContainerGroup_FieldRegistry(t *testing.T) {
	assertStructFields(t, reflect.TypeFor[ContainerGroup](), []string{"ID", "Name", "Status", "CreationTime", "SucceededTime", "FailedTime", "Containers", "InitContainers", "Events"})
	assertStructFields(t, reflect.TypeFor[ContainerStatus](), []string{"Name", "CurrentState"})
	assertStructFields(t, reflect.TypeFor[ContainerState](), []string{"State", "StartTime", "FinishTime", "ExitCode", "Reason", "Message"})
	assertStructFields(t, reflect.TypeFor[ContainerGroupEvent](), []string{"Type", "Reason", "Message", "Count", "LastTimestamp"})
}

func TestClient_CreateContainerGroupRejectsInvalidRequest(t *testing.T) {
	for _, testCase := range invalidCreateRequestCases() {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			client := newTestClient(t, runner)
			request := validCreateRequest()
			testCase.mutate(&request)
			if _, err := client.CreateContainerGroup(context.Background(), request); err == nil {
				t.Fatal("CreateContainerGroup() error = nil")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none", runner.calls)
			}
		})
	}
}

func TestClient_CreateContainerGroupRedactsEnvironmentValues(t *testing.T) {
	const secret = "super-secret-value"
	client := newTestClient(t, &fakeCommandRunner{err: errors.New("CLI stderr: " + secret)})
	request := validCreateRequest()
	request.InitContainer.Environment = map[string]string{"TOKEN": secret}
	_, err := client.CreateContainerGroup(context.Background(), request)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("CreateContainerGroup() error = %v, want redacted secret", err)
	}
}

func TestExecRunner_CapturesStderrAndWrapsExitError(t *testing.T) {
	_, err := (execRunner{}).Run(context.Background(), "sh", "-c", "echo runner-stderr >&2; exit 7")
	if err == nil || !strings.Contains(err.Error(), "runner-stderr") {
		t.Fatalf("Run() error = %v, want captured stderr", err)
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("Run() error = %T, want wrapped *exec.ExitError", err)
	}
}

func newTestClient(t *testing.T, runner CommandRunner) *Client {
	t.Helper()
	client, err := NewWithRunner(testConfig(), runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	client.wait = func(context.Context, time.Duration) error { return nil }
	client.newClientToken = func() (string, error) { return testClientToken, nil }
	return client
}

func testConfig() Config {
	return Config{
		Binary: "aliyun", RegionID: "cn-hangzhou", VSwitchID: "vsw-1", SecurityGroupID: "sg-1",
		WorkerRoleName: "worker-role", Profile: "ci",
		Deadline:     time.Hour,
		SpotStrategy: SpotStrategyAsPriceGo, SpotDurationHours: 1,
	}
}

func validCreateRequest() CreateRequest {
	return CreateRequest{
		ContainerGroupName:   "shard-1",
		ContainerName:        "worker",
		ImageCacheSnapshotID: "snap-test-cache",
		MainImage:            testMainImageDigest,
		InitImage:            testInitImageDigest,
		Resources:            Resources{CPU: 4, MemoryGiB: 8},
		Command:              []string{"/runner", "execute"},
		Args:                 []string{"--shard", "one"},
		Environment:          map[string]string{"TASK_MODE": "execute"},
		Tags:                 map[string]string{"workload": "test"},
		InitContainer: InitContainer{
			Name:        "materializer",
			Command:     []string{"/runner", "materialize"},
			Args:        []string{"--source", "/input/source", "--work", "/workspace"},
			Environment: map[string]string{"Z_INIT": "z-init", "A_INIT": "a-init"},
		},
		SourceVolume: EmptyDirVolume{Name: "source-data"},
		WorkVolume:   EmptyDirVolume{Name: "work-data"},
		TempVolume:   EmptyDirVolume{Name: "temp-data"},
		MainVolumeMounts: []VolumeMount{
			{Name: "source-data", MountPath: "/input/source", ReadOnly: true},
			{Name: "work-data", MountPath: "/workspace"},
			{Name: "temp-data", MountPath: "/tmp"},
		},
		InitVolumeMounts: []VolumeMount{
			{Name: "source-data", MountPath: "/input/source"},
			{Name: "work-data", MountPath: "/workspace"},
		},
	}
}

func assertStructFields(t *testing.T, structType reflect.Type, expected []string) {
	t.Helper()
	actual := make([]string, 0, structType.NumField())
	for field := range structType.Fields() {
		actual = append(actual, field.Name)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s fields = %v, want %v", structType.Name(), actual, expected)
	}
}

const (
	testMainImageDigest = "registry.example/remote-builder@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testInitImageDigest = "registry.example/accepted-gate@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	testClientToken     = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
)

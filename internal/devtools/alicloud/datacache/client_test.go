package datacache

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls     [][]string
	responses [][]byte
	runErrors []error
	err       error
}

func (runner *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string{name}, args...))
	if len(runner.runErrors) > 0 {
		err := runner.runErrors[0]
		runner.runErrors = runner.runErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	if runner.err != nil {
		return nil, runner.err
	}
	response := runner.responses[0]
	runner.responses = runner.responses[1:]
	return response, nil
}

func TestClientRetriesTransientDescribeFailure(t *testing.T) {
	for name, transient := range map[string]error{
		"header timeout":  errors.New("init client failed Post \"https://sts.example.invalid?AccessKeyId=test\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)"),
		"user throttling": errors.New(`init client failed refresh session token failed: {"Message":"Request was denied due to user flow control.","Code":"Throttling.User"}`),
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{
				responses: [][]byte{[]byte(`{"DataCaches":[]}`)},
				runErrors: []error{transient},
			}
			client := newTestClient(t, runner)
			var waits []time.Duration
			client.wait = func(_ context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return nil
			}
			caches, err := client.Describe(context.Background(), "edc-missing")
			if err != nil || len(caches) != 0 {
				t.Fatalf("Describe() = %#v, %v", caches, err)
			}
			if len(runner.calls) != 2 || !reflect.DeepEqual(waits, []time.Duration{initialRetryDelay}) {
				t.Fatalf("calls = %d, waits = %v", len(runner.calls), waits)
			}
		})
	}
}

func TestClientFindByPathUsesScopedStableFilters(t *testing.T) {
	runner := &fakeRunner{responses: [][]byte{[]byte(
		`{"DataCaches":[{"DataCacheId":"edc-stale","Name":"baseline-22","Status":"Available","Bucket":"super-dolphin-ci","Path":"/super-dolphin/ci/baselines/22","Size":20}]}`,
	)}}
	client := newTestClient(t, runner)

	caches, err := client.FindByPath(
		context.Background(),
		"super-dolphin-ci",
		"/super-dolphin/ci/baselines/22",
		map[string]string{"owner": "super-dolphin-ci", "generation": "22"},
	)
	if err != nil || len(caches) != 1 || caches[0].ID != "edc-stale" {
		t.Fatalf("FindByPath() = %#v, %v", caches, err)
	}
	want := []string{
		"aliyun", "eci", "DescribeDataCaches", "--RegionId", "cn-shenzhen", "--profile", "ci",
		"--Bucket", "super-dolphin-ci", "--Path", "/super-dolphin/ci/baselines/22",
		"--Tag.1.Key", "generation", "--Tag.1.Value", "22",
		"--Tag.2.Key", "owner", "--Tag.2.Value", "super-dolphin-ci",
		"--Limit", "20",
	}
	if !reflect.DeepEqual(runner.calls, [][]string{want}) {
		t.Fatalf("CLI calls = %#v, want %#v", runner.calls, [][]string{want})
	}
}

func TestClientFindByPathRejectsPaginatedOrDriftedResponse(t *testing.T) {
	for name, response := range map[string]string{
		"paginated": `{"DataCaches":[],"NextToken":"more"}`,
		"drifted":   `{"DataCaches":[{"DataCacheId":"edc-other","Name":"baseline-23","Status":"Available","Bucket":"super-dolphin-ci","Path":"/super-dolphin/ci/baselines/23","Size":20}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{responses: [][]byte{[]byte(response)}}
			client := newTestClient(t, runner)
			_, err := client.FindByPath(
				context.Background(),
				"super-dolphin-ci",
				"/super-dolphin/ci/baselines/22",
				map[string]string{"owner": "super-dolphin-ci"},
			)
			if err == nil {
				t.Fatal("FindByPath() error = nil")
			}
		})
	}
}

func TestClientRenewRetriesTransientCredentialFailureWithoutCreatingCache(t *testing.T) {
	runner := &fakeRunner{
		responses: [][]byte{[]byte(`{"RequestId":"request-renewed"}`)},
		runErrors: []error{errors.New(
			`init client failed refresh session token failed: Post "https://sts.example.invalid": tls handshake timeout`,
		)},
	}
	client := newTestClient(t, runner)
	var waits []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}

	if err := client.Renew(context.Background(), "edc-created", 2, "renew-token-1"); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if !reflect.DeepEqual(waits, []time.Duration{initialRetryDelay}) {
		t.Fatalf("retry waits = %v", waits)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %#v, want two UpdateDataCache calls", runner.calls)
	}
	for _, call := range runner.calls {
		if call[2] != "UpdateDataCache" {
			t.Fatalf("CLI action = %q, want UpdateDataCache", call[2])
		}
	}
}

func TestClientRenewBoundsTransientCredentialRetriesWithoutCreatingCache(t *testing.T) {
	const wantAttempts = 12
	runner := &fakeRunner{err: errors.New(
		`init client failed refresh session token failed: Post "https://sts.example.invalid": tls handshake timeout`,
	)}
	client := newTestClient(t, runner)
	var waits []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}

	err := client.Renew(context.Background(), "edc-created", 2, "renew-token-1")
	if err == nil {
		t.Fatal("Renew() error = nil")
	}
	if len(runner.calls) != wantAttempts {
		t.Fatalf("runner calls = %#v, want %d bounded UpdateDataCache calls", runner.calls, wantAttempts)
	}
	wantWaits := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
	}
	if !reflect.DeepEqual(waits, wantWaits) {
		t.Fatalf("retry waits = %v, want %v", waits, wantWaits)
	}
	for index, call := range runner.calls {
		if call[2] != "UpdateDataCache" {
			t.Fatalf("CLI action = %q, want UpdateDataCache", call[2])
		}
		if token, found := commandArgument(call, "--ClientToken"); !found || token != "renew-token-1" {
			t.Fatalf("UpdateDataCache client token = %q, %t; want renew-token-1, true", token, found)
		}
		if index > 0 && !reflect.DeepEqual(call, runner.calls[0]) {
			t.Fatalf("UpdateDataCache arguments drifted: %#v, want %#v", call, runner.calls[0])
		}
	}
}

func TestClientRenewFailsFastForPermanentCredentialFailure(t *testing.T) {
	runner := &fakeRunner{err: errors.New(`init client failed refresh session token failed: InvalidAccessKeyId.NotFound`)}
	client := newTestClient(t, runner)
	client.wait = func(_ context.Context, _ time.Duration) error {
		t.Fatal("Renew() retried a permanent credential failure")
		return nil
	}

	err := client.Renew(context.Background(), "edc-created", 2, "renew-token-1")
	if err == nil {
		t.Fatal("Renew() error = nil")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %#v, want one UpdateDataCache call", runner.calls)
	}
	if runner.calls[0][2] != "UpdateDataCache" {
		t.Fatalf("CLI action = %q, want UpdateDataCache", runner.calls[0][2])
	}
}

func TestClientCreateRetriesWithoutArgumentDrift(t *testing.T) {
	runner := &fakeRunner{
		responses: [][]byte{[]byte(`{"DataCacheId":"edc-created"}`)},
		runErrors: repeatError(errors.New("tls handshake timeout"), maxCLIAttempts-1),
	}
	client := newTestClient(t, runner)
	client.wait = func(_ context.Context, _ time.Duration) error { return nil }

	request := validCreateRequest()
	if _, err := client.Create(context.Background(), request); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(runner.calls) != maxCLIAttempts {
		t.Fatalf("runner calls = %d, want %d", len(runner.calls), maxCLIAttempts)
	}
	for _, call := range runner.calls[1:] {
		if !reflect.DeepEqual(call, runner.calls[0]) {
			t.Fatalf("CreateDataCache arguments drifted: %#v, want %#v", call, runner.calls[0])
		}
	}
}

func TestClientStopsRetryingWhenContextIsCanceled(t *testing.T) {
	runner := &fakeRunner{err: errors.New("tls handshake timeout")}
	client := newTestClient(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Describe(ctx, "edc-missing")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Describe() error = %v, want context cancellation", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %#v, want one call before cancellation", runner.calls)
	}
}

func TestClient_DataCacheLifecycle(t *testing.T) {
	runner := &fakeRunner{responses: [][]byte{
		[]byte(`{"DataCacheId":"edc-created","RequestId":"request-created"}`),
		[]byte(`{"DataCaches":[{"DataCacheId":"edc-created","Name":"baseline-1","Status":"Available","Bucket":"super-dolphin-ci","Path":"/super-dolphin/ci/baselines/1","Size":20}],"TotalCount":1}`),
		[]byte(`{"RequestId":"request-renewed"}`),
		[]byte(`{"RequestId":"request-deleted"}`),
	}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	created, err := client.Create(context.Background(), request)
	if err != nil || created.ID != "edc-created" {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	caches, err := client.Describe(context.Background(), "edc-created")
	if err != nil || len(caches) != 1 || caches[0].Status != StatusAvailable {
		t.Fatalf("Describe() = %#v, %v", caches, err)
	}
	if err := client.Renew(context.Background(), "edc-created", 2, "renew-token-1"); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if err := client.Delete(context.Background(), "edc-created", request.Bucket, request.Path); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	want := [][]string{
		{
			"aliyun", "eci", "CreateDataCache", "--RegionId", "cn-shenzhen", "--profile", "ci",
			"--VSwitchId", "vsw-1", "--SecurityGroupId", "sg-1",
			"--Bucket", "super-dolphin-ci", "--Path", "/super-dolphin/ci/baselines/1",
			"--Name", "baseline-1", "--Size", "20", "--RetentionDays", "2",
			"--ClientToken", "baseline-token-1",
			"--DataSource.Type", "OSS",
			"--DataSource.Options.#6#bucket", "source-bucket",
			"--DataSource.Options.#3#url", "oss-cn-shenzhen-internal.aliyuncs.com",
			"--DataSource.Options.#4#path", "/baseline-artifacts/1",
			"--DataSource.Options.#7#ramRole", "worker-role",
			"--Tag.1.Key", "generation", "--Tag.1.Value", "1",
			"--Tag.2.Key", "owner", "--Tag.2.Value", "super-dolphin-ci",
			"--method", "POST", "--force",
		},
		{
			"aliyun", "eci", "DescribeDataCaches", "--RegionId", "cn-shenzhen", "--profile", "ci",
			"--DataCacheId.1", "edc-created", "--Limit", "20",
		},
		{
			"aliyun", "eci", "UpdateDataCache", "--RegionId", "cn-shenzhen", "--profile", "ci",
			"--DataCacheId", "edc-created", "--RetentionDays", "2",
			"--ClientToken", "renew-token-1",
		},
		{
			"aliyun", "eci", "DeleteDataCache", "--RegionId", "cn-shenzhen", "--profile", "ci",
			"--DataCacheId", "edc-created", "--Bucket", "super-dolphin-ci",
			"--Path", "/super-dolphin/ci/baselines/1",
		},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("CLI calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClientCreateUsesForcedFlatDataSourceArguments(t *testing.T) {
	runner := &fakeRunner{responses: [][]byte{[]byte(`{"DataCacheId":"edc-created"}`)}}
	client := newTestClient(t, runner)

	if _, err := client.Create(context.Background(), validCreateRequest()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %#v, want one call", runner.calls)
	}

	args := runner.calls[0]
	for flag, want := range map[string]string{
		"--DataSource.Type":               "OSS",
		"--DataSource.Options.#6#bucket":  "source-bucket",
		"--DataSource.Options.#3#url":     "oss-cn-shenzhen-internal.aliyuncs.com",
		"--DataSource.Options.#4#path":    "/baseline-artifacts/1",
		"--DataSource.Options.#7#ramRole": "worker-role",
		"--method":                        "POST",
	} {
		if value, found := commandArgument(args, flag); !found || value != want {
			t.Fatalf("%s = %q, %t; want %q, true", flag, value, found, want)
		}
	}
	if _, found := commandArgument(args, "--data-source"); found {
		t.Fatal("Create() submitted plugin JSON instead of verified flat fields")
	}
}

func TestClientRedactsSensitiveCLIHeaders(t *testing.T) {
	secrets := []string{"STS.sensitive-id", "sensitive-security-token", "sensitive-signature"}
	runner := &fakeRunner{err: errors.New(
		"SignatureDoesNotMatch\n" +
			"x-acs-accesskey-id:" + secrets[0] + "\n" +
			"x-acs-security-token:" + secrets[1] + "\n" +
			"Signature=" + secrets[2],
	)}
	client := newTestClient(t, runner)

	_, err := client.Create(context.Background(), validCreateRequest())
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Create() error leaked sensitive value %q", secret)
		}
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("Create() error = %q, want redaction marker", err)
	}
}

func TestCreateRequestFieldRegistry(t *testing.T) {
	assertFields(t, reflect.TypeFor[CreateRequest](), []string{
		"Name", "Bucket", "Path", "SizeGiB", "RetentionDays", "ClientToken", "Source", "Tags",
	})
	assertFields(t, reflect.TypeFor[OSSDataSource](), []string{"Bucket", "Endpoint", "Path", "RoleName"})
	assertFields(t, reflect.TypeFor[DataCache](), []string{"ID", "Name", "Status", "Bucket", "Path", "SizeGiB"})
}

func TestClientRejectsInvalidCreateRequest(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*CreateRequest)
	}{
		{"missing name", func(request *CreateRequest) { request.Name = "" }},
		{"reserved bucket", func(request *CreateRequest) { request.Bucket = "eci-system" }},
		{"relative path", func(request *CreateRequest) { request.Path = "baseline" }},
		{"small size", func(request *CreateRequest) { request.SizeGiB = 0 }},
		{"long retention", func(request *CreateRequest) { request.RetentionDays = 8 }},
		{"missing token", func(request *CreateRequest) { request.ClientToken = "" }},
		{"missing OSS bucket", func(request *CreateRequest) { request.Source.Bucket = "" }},
		{"public OSS endpoint", func(request *CreateRequest) {
			request.Source.Endpoint = "oss-cn-shenzhen.aliyuncs.com"
		}},
		{"root OSS path", func(request *CreateRequest) { request.Source.Path = "/" }},
		{"missing role", func(request *CreateRequest) { request.Source.RoleName = "" }},
		{"too many tags", func(request *CreateRequest) {
			request.Tags = map[string]string{
				"1": "1", "2": "2", "3": "3", "4": "4", "5": "5", "6": "6",
				"7": "7", "8": "8", "9": "9", "10": "10", "11": "11",
			}
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &fakeRunner{}
			client := newTestClient(t, runner)
			request := validCreateRequest()
			testCase.mutate(&request)
			if _, err := client.Create(context.Background(), request); err == nil {
				t.Fatal("Create() error = nil")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none", runner.calls)
			}
		})
	}
}

func TestClientFailsClosedOnCommandAndResponseErrors(t *testing.T) {
	client := newTestClient(t, &fakeRunner{err: errors.New("permission denied")})
	if _, err := client.Create(context.Background(), validCreateRequest()); err == nil {
		t.Fatal("Create() error = nil")
	}
	client = newTestClient(t, &fakeRunner{responses: [][]byte{[]byte(`{}`)}})
	if _, err := client.Create(context.Background(), validCreateRequest()); err == nil {
		t.Fatal("Create() missing ID error = nil")
	}
	client = newTestClient(t, &fakeRunner{responses: [][]byte{[]byte(`{}`)}})
	if err := client.Renew(context.Background(), "edc-created", 2, "renew-token"); err == nil {
		t.Fatal("Renew() missing RequestId error = nil")
	}
	client = newTestClient(t, &fakeRunner{})
	for _, input := range []struct {
		id        string
		retention int
		token     string
	}{
		{"invalid", 2, "renew-token"},
		{"edc-created", 0, "renew-token"},
		{"edc-created", 2, ""},
	} {
		if err := client.Renew(context.Background(), input.id, input.retention, input.token); err == nil {
			t.Fatalf("Renew(%q, %d, %q) error = nil", input.id, input.retention, input.token)
		}
	}
}

func TestClientDescribeReturnsEmptyAfterCacheDeletion(t *testing.T) {
	client := newTestClient(t, &fakeRunner{responses: [][]byte{[]byte(`{"DataCaches":[]}`)}})
	caches, err := client.Describe(context.Background(), "edc-missing")
	if err != nil {
		t.Fatalf("Describe() error = %v", err)
	}
	if len(caches) != 0 {
		t.Fatalf("Describe() caches = %#v", caches)
	}
}

func newTestClient(t *testing.T, runner CommandRunner) *Client {
	t.Helper()
	client, err := NewWithRunner(Config{
		Binary: "aliyun", RegionID: "cn-shenzhen", VSwitchID: "vsw-1",
		SecurityGroupID: "sg-1", Profile: "ci",
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func validCreateRequest() CreateRequest {
	return CreateRequest{
		Name: "baseline-1", Bucket: "super-dolphin-ci",
		Path: "/super-dolphin/ci/baselines/1", SizeGiB: 20, RetentionDays: 2,
		ClientToken: "baseline-token-1",
		Source: OSSDataSource{
			Bucket: "source-bucket", Endpoint: "oss-cn-shenzhen-internal.aliyuncs.com",
			Path: "/baseline-artifacts/1", RoleName: "worker-role",
		},
		Tags: map[string]string{"owner": "super-dolphin-ci", "generation": "1"},
	}
}

func repeatError(err error, count int) []error {
	errors := make([]error, count)
	for index := range errors {
		errors[index] = err
	}
	return errors
}

func assertFields(t *testing.T, structType reflect.Type, expected []string) {
	t.Helper()
	actual := make([]string, 0, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		actual = append(actual, structType.Field(index).Name)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s fields = %v, want %v", structType.Name(), actual, expected)
	}
}

func commandArgument(args []string, flag string) (string, bool) {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag {
			return args[index+1], true
		}
	}
	return "", false
}

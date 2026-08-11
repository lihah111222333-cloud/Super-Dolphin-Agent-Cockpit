package eci

import (
	"context"
	"strings"
	"testing"
)

func TestConfirmContainerGroupAbsentRequiresExplicitEmptyResponse(t *testing.T) {
	tests := []struct {
		name      string
		response  []byte
		want      bool
		wantError string
	}{
		{name: "empty collection", response: []byte(`{"ContainerGroups":[]}`), want: true},
		{name: "group still present", response: []byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-1","Status":"Terminated"}]}`)},
		{name: "missing collection", response: []byte(`{}`), wantError: "missing ContainerGroups"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{testCase.response}})
			absent, err := client.ConfirmContainerGroupAbsent(context.Background(), "eci-1")
			if absent != testCase.want {
				t.Fatalf("ConfirmContainerGroupAbsent() absent=%t, want %t", absent, testCase.want)
			}
			if testCase.wantError == "" && err != nil {
				t.Fatalf("ConfirmContainerGroupAbsent() error = %v", err)
			}
			if testCase.wantError != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantError)) {
				t.Fatalf("ConfirmContainerGroupAbsent() error = %v, want %q", err, testCase.wantError)
			}
		})
	}
}

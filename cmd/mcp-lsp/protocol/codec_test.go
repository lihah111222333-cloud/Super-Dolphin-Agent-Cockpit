package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodeResponseTracksResultPresence(t *testing.T) {
	tests := []struct {
		name       string
		wire       string
		wantErr    bool
		wantResult string
	}{
		{name: "null result is legal", wire: `{"jsonrpc":"2.0","id":1,"result":null}`, wantResult: "null"},
		{name: "missing result and error fails", wire: `{"jsonrpc":"2.0","id":1}`, wantErr: true},
		{name: "error response is legal", wire: `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"failed"}}`},
		{name: "result and error fail", wire: `{"jsonrpc":"2.0","id":1,"result":null,"error":{"code":-32603,"message":"failed"}}`, wantErr: true},
		{name: "null error fails", wire: `{"jsonrpc":"2.0","id":1,"error":null}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := DecodeResponse([]byte(test.wire))
			if (err != nil) != test.wantErr {
				t.Fatalf("DecodeResponse error=%v wantErr=%v", err, test.wantErr)
			}
			if err == nil && string(response.ID) != "1" {
				t.Fatalf("response id=%s want=1", response.ID)
			}
			if err == nil && test.wantResult != string(response.Result) {
				t.Fatalf("result=%q want=%q", response.Result, test.wantResult)
			}
		})
	}
}

func TestParameterInformationResultDecodesLabelUnion(t *testing.T) {
	tests := []struct {
		name        string
		wire        string
		wantLabel   string
		wantOffsets []int
	}{
		{name: "string label", wire: `{"label":"value"}`, wantLabel: "value"},
		{name: "offset label", wire: `{"label":[12,17]}`, wantOffsets: []int{12, 17}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got ParameterInformationResult
			if err := json.Unmarshal([]byte(test.wire), &got); err != nil {
				t.Fatalf("decode parameter label: %v", err)
			}
			if got.Label != test.wantLabel || !reflect.DeepEqual(got.LabelOffsets, test.wantOffsets) {
				t.Fatalf("decoded parameter = %#v, want label=%q offsets=%v", got, test.wantLabel, test.wantOffsets)
			}
		})
	}
}

package protocol

import "testing"

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

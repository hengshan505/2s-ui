package service

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSubCustomHeaders(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []SubCustomHeader
		wantErr []string // substrings the error must carry; nil = no error
	}{
		{"empty", "", nil, nil},
		{"blank and comment lines", "  \n\t\r\n# announce: off\n", nil, nil},
		{
			"spaces trimmed, CRLF tolerated",
			"\tannounce :  hello \r\nannounce-url: https://t.me/x\r\n",
			[]SubCustomHeader{{"announce", "hello"}, {"announce-url", "https://t.me/x"}},
			nil,
		},
		{
			"the value keeps its colons",
			"Profile-Web-Page-Url: https://sub.example.com:8443/login?next=https://a.com",
			[]SubCustomHeader{{"Profile-Web-Page-Url", "https://sub.example.com:8443/login?next=https://a.com"}},
			nil,
		},
		{
			"non-ASCII value",
			"announce: 本月流量已重置",
			[]SubCustomHeader{{"announce", "本月流量已重置"}},
			nil,
		},
		{
			// Every bad line is named, and the good ones are still read: the
			// sub server serves those from a value stored before the check.
			"bad lines are reported, good ones kept",
			"no colon here\nBad Name: x\nempty:\nGood: ok\n: x",
			[]SubCustomHeader{{"Good", "ok"}},
			[]string{"line 1:", "line 2:", "line 3: empty value", "line 5:"},
		},
		{
			"headers the panel sets itself are refused, in any case",
			"profile-title: Mine\nSubscription-Userinfo: upload=0\nX-Ok: 1",
			[]SubCustomHeader{{"X-Ok", "1"}},
			[]string{"line 1: Profile-Title is set by the panel", "line 2: Subscription-Userinfo"},
		},
		{
			"framing headers are refused",
			"Content-Encoding: gzip\nTransfer-Encoding: chunked\nContent-Length: 1",
			nil,
			[]string{"line 1: Content-Encoding", "line 2: Transfer-Encoding", "line 3: Content-Length"},
		},
		{
			"a repeated name is refused, not silently overwritten",
			"announce: one\nANNOUNCE: two",
			[]SubCustomHeader{{"announce", "one"}},
			[]string{"line 2: Announce is already set on line 1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSubCustomHeaders(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("headers = %+v, want %+v", got, tt.want)
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error naming %q", tt.wantErr)
			}
			for _, part := range tt.wantErr {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("error %q does not mention %q", err, part)
				}
			}
		})
	}
}

package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/shenaba/2s-ui/util/common"

	"golang.org/x/net/http/httpguts"
)

// SubCustomHeader is one operator-defined response header from the
// subCustomHeaders setting -- `announce` and `announce-url` for v2RayTun's
// notice banner are what it is for.
type SubCustomHeader struct {
	Name  string
	Value string
}

// reservedSubHeaders cannot be set from subCustomHeaders. The first group the
// sub server computes itself, per client or per request, and sets after the
// custom ones; accepting them would store a line that never reaches anyone.
// The second describe the message framing and encoding, and the gzip
// middleware owns two of them: a value there would corrupt the response rather
// than annotate it.
var reservedSubHeaders = map[string]bool{
	"Subscription-Userinfo":   true,
	"Profile-Update-Interval": true,
	"Profile-Title":           true,
	"Content-Disposition":     true,
	"Content-Type":            true,

	"Content-Length":    true,
	"Content-Encoding":  true,
	"Transfer-Encoding": true,
	"Vary":              true,
	"Connection":        true,
	"Keep-Alive":        true,
	"Upgrade":           true,
	"Trailer":           true,
	"Te":                true,
}

// ParseSubCustomHeaders reads the subCustomHeaders setting: one "Name: value"
// per line, the value running to the end of the line (colons included), blank
// lines and lines starting with '#' skipped.
//
// It returns every line it could read together with an error naming each one
// it could not. Save refuses on that error, so the operator hears about a typo
// instead of a header silently missing; the sub server serves what was read
// and ignores the rest, since a stored value can predate the check.
func ParseSubCustomHeaders(raw string) ([]SubCustomHeader, error) {
	var headers []SubCustomHeader
	var problems []string
	seen := map[string]int{}
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		problem := ""
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		canonical := http.CanonicalHeaderKey(name)
		switch {
		case !ok:
			problem = "expected \"Name: value\""
		case !httpguts.ValidHeaderFieldName(name):
			problem = fmt.Sprintf("%q is not a valid header name", name)
		case value == "":
			problem = "empty value"
		case !httpguts.ValidHeaderFieldValue(value):
			problem = "the value contains a control character"
		case reservedSubHeaders[canonical]:
			problem = fmt.Sprintf("%s is set by the panel itself", canonical)
		case seen[canonical] > 0:
			problem = fmt.Sprintf("%s is already set on line %d", canonical, seen[canonical])
		}
		if problem != "" {
			problems = append(problems, fmt.Sprintf("line %d: %s", i+1, problem))
			continue
		}
		seen[canonical] = i + 1
		headers = append(headers, SubCustomHeader{Name: name, Value: value})
	}
	if len(problems) > 0 {
		return headers, common.NewErrorf("subCustomHeaders: %s", strings.Join(problems, "; "))
	}
	return headers, nil
}

// GetSubCustomHeaders is read on every subscription request rather than once
// at start, so a saved change applies without restarting the sub server.
func (s *SettingService) GetSubCustomHeaders() ([]SubCustomHeader, error) {
	raw, err := s.getString("subCustomHeaders")
	if err != nil {
		return nil, err
	}
	headers, _ := ParseSubCustomHeaders(raw)
	return headers, nil
}

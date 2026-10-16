package util

import (
	"encoding/base64"
	"strings"
)

// Function to return decoded bytes if a string is Base64 encoded
func StrOrBase64Encoded(str string) string {
	decoded, err := base64.StdEncoding.DecodeString(str)
	if err == nil {
		return string(decoded)
	}
	return str
}

// StrOrBase64AnyEncoded is StrOrBase64Encoded for a string that is *known* to
// be base64, in whichever of the four forms the writer happened to use.
//
// SIP002 specifies base64url without padding for the ss:// userinfo, which
// StdEncoding rejects on both counts, so the panel could not read back the
// links it generates itself. Third-party ss links are base64url too.
//
// Kept separate from StrOrBase64Encoded rather than widening it: that one is
// also handed text that may not be base64 at all -- a naive hostname -- and
// the unpadded alphabets have no padding to fail on, so widening it there
// would start "decoding" plain strings into garbage. An external subscription
// body is safe to hand here: whatever it is when it is not base64 -- a list of
// links, JSON, YAML -- carries a ':' or a '{', which no alphabet accepts.
func StrOrBase64AnyEncoded(str string) string {
	if decoded, err := decodeAnyBase64(str); err == nil {
		return string(decoded)
	}
	return str
}

// decodeAnyBase64 tries the four forms in turn. They cannot disagree: a '+' or
// '/' rules out the URL alphabet and a '-' or '_' the standard one, and a
// string both a padded and a raw form accept has no padding, so both read it
// the same.
func decodeAnyBase64(str string) ([]byte, error) {
	var err error
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
	} {
		var decoded []byte
		if decoded, err = enc.DecodeString(str); err == nil {
			return decoded, nil
		}
	}
	return nil, err
}

// B64StrToByte decodes a string that is known to be base64 -- a vmess payload
// -- in any of the four forms. Payloads from other panels and converters are
// often unpadded, which StdEncoding alone rejected, so the node vanished from
// every subscription that parses links (#1281).
func B64StrToByte(str string) ([]byte, error) {
	return decodeAnyBase64(strings.TrimSpace(str))
}

func ByteToB64Str(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

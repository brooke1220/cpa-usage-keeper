package helper

import "encoding/base64"

func ScopedPushAuthIndex(sourceID, authIndex string) string {
	if authIndex == "" {
		return ""
	}
	return "push." + base64.RawURLEncoding.EncodeToString([]byte(sourceID)) + "." + base64.RawURLEncoding.EncodeToString([]byte(authIndex))
}

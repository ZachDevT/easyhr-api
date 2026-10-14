package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func verifyResendWebhook(secret, messageID, timestamp, signature string, body []byte, now time.Time) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" || messageID == "" || timestamp == "" || signature == "" {
		return false
	}
	unixTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		seconds, parseErr := strconv.ParseInt(timestamp, 10, 64)
		if parseErr != nil {
			return false
		}
		unixTime = time.Unix(seconds, 0)
	}
	if delta := now.Sub(unixTime); delta > 5*time.Minute || delta < -5*time.Minute {
		return false
	}
	encodedSecret := strings.TrimPrefix(secret, "whsec_")
	key, err := base64.StdEncoding.DecodeString(encodedSecret)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encodedSecret)
		if err != nil {
			return false
		}
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(fmt.Sprintf("%s.%s.%s", messageID, timestamp, string(body))))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, part := range strings.Fields(signature) {
		version, value, ok := strings.Cut(part, ",")
		if ok && version == "v1" && hmac.Equal([]byte(value), []byte(expected)) {
			return true
		}
	}
	return false
}

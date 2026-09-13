package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

func writeUnauthorized(writer http.ResponseWriter, request *http.Request, code, detail string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type":      "https://docs.easyhr.example/errors/" + strings.ReplaceAll(code, ".", "-"),
		"title":     "Unauthorized",
		"status":    http.StatusUnauthorized,
		"code":      code,
		"detail":    detail,
		"requestId": request.Header.Get("X-Request-Id"),
	})
}

package identity

import (
	"encoding/json"
	"net/http"
	"strings"
)

func writeProblem(writer http.ResponseWriter, request *http.Request, status int, code, title, detail string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type":      "https://docs.easyhr.example/errors/" + strings.ReplaceAll(code, ".", "-"),
		"title":     title,
		"status":    status,
		"code":      code,
		"detail":    detail,
		"requestId": request.Header.Get("X-Request-Id"),
	})
}

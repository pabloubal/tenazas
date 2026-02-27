package telegram

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

type mockTgServer struct {
	mu           sync.Mutex
	calls        []mockCall
	responseFunc func(method string, payload map[string]interface{}) (interface{}, bool)
}

type mockCall struct {
	Method  string
	Payload map[string]interface{}
}

func (m *mockTgServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var payload map[string]interface{}
	json.NewDecoder(r.Body).Decode(&payload)

	// Extract method from URL /botTOKEN/method
	idx := strings.LastIndex(r.URL.Path, "/")
	method := ""
	if idx != -1 {
		method = r.URL.Path[idx+1:]
	}

	m.calls = append(m.calls, mockCall{Method: method, Payload: payload})

	var result interface{}
	ok := true
	if m.responseFunc != nil {
		result, ok = m.responseFunc(method, payload)
	}

	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
		return
	}

	if result == nil {
		// Default success response
		result = map[string]interface{}{
			"message_id": 12345,
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"result": result,
	})
}

package server

import (
	"net/http"

	"openduanju-server/internal/service"
)

// adminMethodFilter records the HTTP method for the auth middleware. The
// Kratos HTTP transport only exposes the operation, and the session check
// needs to know a read from a write.
func adminMethodFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Admin-Method", r.Method)
		// http.Request.Host is not part of Header, and the Kratos transport
		// only exposes headers, so the Origin check needs it here.
		r.Header.Set("X-Admin-Host", r.Host)
		next.ServeHTTP(w, r)
	})
}

// statusFilter lets a handler choose the HTTP status of its response.
//
// The generated HTTP handlers always answer 200, so the transport records the
// status the service selected and applies it when the body is written. The
// status is only applied on success: an error response keeps the status the
// error carries, because a failed call never claims to be accepted.
func statusFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		holder := &service.StatusHolder{}
		ctx := service.WithStatusHolder(r.Context(), holder)
		next.ServeHTTP(&statusResponseWriter{ResponseWriter: w, holder: holder}, r.WithContext(ctx))
	})
}

// statusResponseWriter applies the status a handler selected. The first
// WriteHeader call wins, so the generated 200 never overwrites the status the
// service chose.
type statusResponseWriter struct {
	http.ResponseWriter
	holder *service.StatusHolder
	wrote  bool
}

// WriteHeader records the status of the response.
func (w *statusResponseWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if w.holder != nil {
		for _, cookie := range w.holder.Cookies {
			w.Header().Add("Set-Cookie", cookie)
		}
		if w.holder.Code != 0 {
			code = w.holder.Code
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write sends the body with the recorded status.
func (w *statusResponseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// Package prpc contains the minimum needed to answer cipd client requests.
package prpc

import (
	"io"
	"mime"
	"net/http"
	"strconv"

	"google.golang.org/protobuf/proto"
)

// Numeric gRPC status codes.
const (
	CodeOK              = 0
	CodeInvalidArgument = 3
	CodeNotFound        = 5
	CodeUnimplemented   = 12
	CodeInternal        = 13
)

const maxRequestSize = 1 << 20

func httpStatus(code int) int {
	switch code {
	case CodeOK:
		return http.StatusOK
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeNotFound:
		return http.StatusNotFound
	case CodeUnimplemented:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

func WriteError(w http.ResponseWriter, code int, msg string) {
	h := w.Header()
	h.Set("X-Prpc-Grpc-Code", strconv.Itoa(code))
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(httpStatus(code))
	_, _ = io.WriteString(w, msg)
}

func WriteMessage(w http.ResponseWriter, m proto.Message) {
	b, err := proto.Marshal(m)
	if err != nil {
		WriteError(w, CodeInternal, "internal error")
		return
	}
	h := w.Header()
	h.Set("X-Prpc-Grpc-Code", strconv.Itoa(CodeOK))
	h.Set("Content-Type", "application/prpc; encoding=binary")
	_, _ = w.Write(b)
}

// Decode reads the request into m. If the request is malformed, it writes an error
// response itself and returns false.
func Decode(w http.ResponseWriter, r *http.Request, m proto.Message) bool {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/prpc" || params["encoding"] != "binary" {
		WriteError(w, CodeInvalidArgument, "only application/prpc; encoding=binary is supported")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestSize))
	if err != nil {
		WriteError(w, CodeInvalidArgument, "cannot read request body")
		return false
	}
	if err := proto.Unmarshal(body, m); err != nil {
		WriteError(w, CodeInvalidArgument, "malformed request")
		return false
	}
	return true
}

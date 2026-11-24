package protojsonx_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/protolayer/protojsonx-go"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
)

func ExampleMarshal() {
	msg, err := structpb.NewStruct(map[string]any{"user": "alice", "count": 3})
	if err != nil {
		panic(err)
	}

	b, err := protojsonx.Marshal(msg)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))

	var out structpb.Struct
	if err := protojsonx.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	fmt.Println(out.Fields["user"].GetStringValue())
	// Output:
	// {"count":3,"user":"alice"}
	// alice
}

func ExampleMarshalEncode() {
	type Event struct {
		ID      string           `json:"id"`
		Payload *structpb.Struct `json:"payload"`
	}

	s, err := structpb.NewStruct(map[string]any{"user": "alice", "count": 3})
	if err != nil {
		panic(err)
	}

	// Define reusable options to stream any proto.Message field through
	// encoding/json/v2 with no wrapper type or code generation.
	opts := json.JoinOptions(
		json.WithMarshalers(json.MarshalToFunc(protojsonx.MarshalEncode)),
		json.WithUnmarshalers(json.UnmarshalFromFunc(protojsonx.UnmarshalDecode)),
	)

	b, err := json.Marshal(Event{ID: "evt_1", Payload: s}, opts)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))

	var out Event
	if err := json.Unmarshal(b, &out, opts); err != nil {
		panic(err)
	}
	fmt.Println(out.ID, out.Payload.Fields["user"].GetStringValue())
	// Output:
	// {"id":"evt_1","payload":{"count":3,"user":"alice"}}
	// evt_1 alice
}

func Example_httpHandler() {
	// DiscardUnknown lets the handler accept newer clients that send extra fields.
	unmarshalOpts := protojsonx.UnmarshalOptions{DiscardUnknown: true}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always cap untrusted request bodies before decoding.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		var req structpb.Struct
		if err := unmarshalOpts.UnmarshalRead(r.Body, &req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// MarshalWrite validates and encodes before writing to w, so an error
		// does not send a partial response with HTTP 200.
		w.Header().Set("Content-Type", "application/json")
		if err := protojsonx.MarshalWrite(w, &req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"user":"alice","count":3}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fmt.Println(rec.Code, rec.Header().Get("Content-Type"))
	fmt.Println(rec.Body.String())
	// Output:
	// 200 application/json
	// {"count":3,"user":"alice"}
}

func Example_httpStream() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")

		// jsontext.NewEncoder writes a newline after each top-level value,
		// which produces newline-delimited JSON without buffering all items.
		enc := jsontext.NewEncoder(w)
		rc := http.NewResponseController(w)

		items := []*durationpb.Duration{
			durationpb.New(500 * time.Millisecond),
			durationpb.New(2 * time.Second),
		}
		for _, item := range items {
			if err := protojsonx.MarshalEncode(enc, item); err != nil {
				// Headers are already sent once enc flushes to w, so just stop.
				return
			}
			_ = rc.Flush()
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/durations", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fmt.Print(rec.Body.String())
	// Output:
	// "0.500s"
	// "2s"
}

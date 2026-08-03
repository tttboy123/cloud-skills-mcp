package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	authSchemeGCPFirebaseSSE = "firebase-sse"

	gcpFirebaseDatabaseScope = "https://www.googleapis.com/auth/firebase.database"
	gcpUserInfoEmailScope    = "https://www.googleapis.com/auth/userinfo.email"

	gcpFirebaseMaxEvents         = 256
	gcpFirebaseMaxTimeoutSeconds = 300
	gcpFirebaseMaxRedirects      = 3
	gcpFirebaseMaxEventBytes     = 1024 * 1024
)

type gcpFirebaseSSEPlan struct {
	MaxEvents        int  `json:"max_events"`
	TimeoutSeconds   int  `json:"timeout_seconds"`
	IncludeKeepAlive bool `json:"include_keep_alive,omitempty"`
}

func validateFirebaseSSEInvocation(invocation Invocation) error {
	if invocation.Mode != ModeRead || !strings.EqualFold(invocation.Method, http.MethodGet) || !strings.EqualFold(invocation.Service, "firebase-database") || !strings.EqualFold(invocation.Operation, "Listen") {
		return fmt.Errorf("Google Cloud Firebase SSE requires the read tool, GET, service firebase-database, and operation Listen")
	}
	if err := validateFirebaseSSETarget(invocation.URL); err != nil {
		return err
	}
	if err := validateFirebaseSSEParameters(invocation.Parameters); err != nil {
		return err
	}
	if len(invocation.Headers) != 0 {
		return fmt.Errorf("Google Cloud Firebase SSE does not accept caller headers")
	}
	if invocation.Body == nil || invocation.BodyFile != "" || invocation.ImageFile != "" || invocation.ProtobufDescriptorFile != "" || invocation.ResponseFile == "" {
		return fmt.Errorf("Google Cloud Firebase SSE requires a finite body plan and response_file; input files are forbidden")
	}
	if invocation.Region != "" || invocation.RegionSet != "" || invocation.Project != "" || invocation.Subscription != "" || invocation.Audience != "" || invocation.APIVersion != "" || invocation.AuthVersion != "" || invocation.PayloadMode != "" || invocation.ChecksumAlgorithm != "" || invocation.StreamChunkBytes != 0 || invocation.StreamIntervalMS != 0 || invocation.StreamUserID != "" || invocation.StreamFormat != 0 {
		return fmt.Errorf("Google Cloud Firebase SSE does not accept cross-provider, REST payload, or stream transport controls")
	}
	_, err := parseFirebaseSSEPlan(invocation.Body)
	return err
}

func validateFirebaseSSETarget(rawURL string) error {
	target, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.Hostname() == "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" {
		return fmt.Errorf("Google Cloud Firebase SSE requires an exact query-free HTTPS database URL")
	}
	if target.Port() != "" && target.Port() != "443" {
		return fmt.Errorf("Google Cloud Firebase SSE endpoint port must be 443")
	}
	if !isOfficialFirebaseDatabaseHost(target.Hostname()) {
		return fmt.Errorf("Google Cloud Firebase SSE requires an official Realtime Database host")
	}
	if len(target.EscapedPath()) == 0 || len(target.EscapedPath()) > 4096 || !strings.HasSuffix(strings.ToLower(target.EscapedPath()), ".json") {
		return fmt.Errorf("Google Cloud Firebase SSE database path must end in .json")
	}
	for _, segment := range strings.Split(strings.ToLower(target.Path), "/") {
		if segment == ".settings" {
			return fmt.Errorf("Google Cloud Firebase SSE does not expose reserved database settings")
		}
	}
	return nil
}

func isOfficialFirebaseDatabaseHost(value string) bool {
	if strings.HasSuffix(value, ".") {
		return false
	}
	host := strings.ToLower(value)
	if suffix := ".firebaseio.com"; strings.HasSuffix(host, suffix) {
		name := strings.TrimSuffix(host, suffix)
		return !strings.Contains(name, ".") && endpointLabelPattern.MatchString(name)
	}
	if suffix := ".firebasedatabase.app"; strings.HasSuffix(host, suffix) {
		prefix := strings.TrimSuffix(host, suffix)
		parts := strings.Split(prefix, ".")
		if len(parts) != 2 || !endpointLabelPattern.MatchString(parts[0]) {
			return false
		}
		return parts[1] == "europe-west1" || parts[1] == "asia-southeast1"
	}
	return false
}

func validateFirebaseSSEParameters(parameters map[string]any) error {
	allowed := map[string]bool{
		"orderBy": true, "limitToFirst": true, "limitToLast": true,
		"startAt": true, "endAt": true, "equalTo": true,
	}
	filter := false
	for name, raw := range parameters {
		if !allowed[name] {
			return fmt.Errorf("unsupported Google Cloud Firebase SSE query parameter %q", name)
		}
		values, err := stringValues(raw)
		if err != nil || len(values) != 1 || values[0] == "" || len(values[0]) > 1024 || containsControlCharacter(values[0]) {
			return fmt.Errorf("Google Cloud Firebase SSE query parameter %q must be one bounded scalar", name)
		}
		if name != "orderBy" {
			filter = true
		}
		if name == "orderBy" {
			var order string
			if json.Unmarshal([]byte(values[0]), &order) != nil || order == "" || len(order) > 768 || containsControlCharacter(order) {
				return fmt.Errorf("Google Cloud Firebase SSE orderBy must be one bounded JSON string")
			}
		}
		if name == "limitToFirst" || name == "limitToLast" {
			limit, parseErr := strconv.Atoi(values[0])
			if parseErr != nil || limit < 1 || limit > 1_000_000 {
				return fmt.Errorf("Google Cloud Firebase SSE %s must be an integer between 1 and 1000000", name)
			}
		}
	}
	if parameters["limitToFirst"] != nil && parameters["limitToLast"] != nil {
		return fmt.Errorf("Google Cloud Firebase SSE accepts only one limit direction")
	}
	if filter && parameters["orderBy"] == nil {
		return fmt.Errorf("Google Cloud Firebase SSE filters require orderBy")
	}
	return nil
}

func containsControlCharacter(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func parseFirebaseSSEPlan(body any) (gcpFirebaseSSEPlan, error) {
	if body == nil || firebaseContainsCredentialField(body) {
		return gcpFirebaseSSEPlan{}, fmt.Errorf("Google Cloud Firebase SSE requires a credential-free finite plan")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRequestPayloadBytes {
		return gcpFirebaseSSEPlan{}, fmt.Errorf("Google Cloud Firebase SSE plan must be bounded JSON")
	}
	var plan gcpFirebaseSSEPlan
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || ensureJSONDecoderEOF(decoder) != nil {
		return gcpFirebaseSSEPlan{}, fmt.Errorf("Google Cloud Firebase SSE body does not match the finite plan schema")
	}
	if plan.MaxEvents < 1 || plan.MaxEvents > gcpFirebaseMaxEvents {
		return gcpFirebaseSSEPlan{}, fmt.Errorf("Google Cloud Firebase SSE max_events must be between 1 and %d", gcpFirebaseMaxEvents)
	}
	if plan.TimeoutSeconds < 1 || plan.TimeoutSeconds > gcpFirebaseMaxTimeoutSeconds {
		return gcpFirebaseSSEPlan{}, fmt.Errorf("Google Cloud Firebase SSE timeout_seconds must be between 1 and %d", gcpFirebaseMaxTimeoutSeconds)
	}
	return plan, nil
}

func invokeGCPFirebaseSSE(ctx context.Context, adapter *GCPRESTAdapter, invocation Invocation) (InvocationResult, error) {
	if err := validateFirebaseSSEInvocation(invocation); err != nil {
		return InvocationResult{}, err
	}
	plan, _ := parseFirebaseSSEPlan(invocation.Body)
	target, _ := url.Parse(invocation.URL)
	if err := addQueryParameters(target, invocation.Parameters); err != nil {
		return InvocationResult{}, fmt.Errorf("build Google Cloud Firebase SSE query: %w", err)
	}
	sink, err := newWebSocketOutputSink(invocation, adapter.config.MaxBodyBytes, "Google Cloud Firebase SSE")
	if err != nil {
		return InvocationResult{}, err
	}
	defer sink.abort()
	token, err := adapter.config.FirebaseTokens.Token(ctx)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load Google Cloud Firebase ADC credential: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return InvocationResult{}, fmt.Errorf("Google Cloud Firebase ADC returned an empty access token")
	}
	streamContext, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	response, err := openFirebaseSSE(streamContext, adapter.config.FirebaseHTTP, target, token)
	if err != nil {
		return InvocationResult{}, err
	}
	defer response.Body.Close()
	requestID := responseRequestID(response.Header)
	written, err := readFirebaseSSE(streamContext, response.Body, plan, sink)
	if err != nil {
		if streamContext.Err() == context.DeadlineExceeded && written > 0 {
			// The caller-selected finite observation window is a normal terminal condition.
		} else {
			return InvocationResult{}, err
		}
	}
	if written == 0 {
		return InvocationResult{}, fmt.Errorf("Google Cloud Firebase SSE produced no data events before termination")
	}
	output, err := sink.finish(requestID)
	if err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Output: output, RequestID: requestID}, nil
}

func openFirebaseSSE(ctx context.Context, client HTTPDoer, initial *url.URL, token string) (*http.Response, error) {
	current := cloneURL(initial)
	expectedPath := current.EscapedPath()
	expectedQuery := current.Query().Encode()
	for redirects := 0; ; redirects++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build Google Cloud Firebase SSE request: %w", err)
		}
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("Google Cloud Firebase SSE request")
		}
		if response == nil {
			return nil, fmt.Errorf("Google Cloud Firebase SSE returned no response")
		}
		if response.StatusCode == http.StatusTemporaryRedirect {
			if response.Body != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				_ = response.Body.Close()
			}
			if redirects >= gcpFirebaseMaxRedirects {
				return nil, fmt.Errorf("Google Cloud Firebase SSE exceeded the redirect limit")
			}
			location, err := response.Location()
			if err != nil {
				return nil, fmt.Errorf("Google Cloud Firebase SSE returned an invalid redirect")
			}
			next := current.ResolveReference(location)
			if err := validateFirebaseSSERedirect(next, expectedPath, expectedQuery); err != nil {
				return nil, err
			}
			current = next
			continue
		}
		if response.Body == nil {
			return nil, fmt.Errorf("Google Cloud Firebase SSE returned an empty response")
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			return nil, fmt.Errorf("Google Cloud Firebase SSE returned HTTP %d", response.StatusCode)
		}
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || !strings.EqualFold(mediaType, "text/event-stream") {
			_ = response.Body.Close()
			return nil, fmt.Errorf("Google Cloud Firebase SSE returned an invalid content type")
		}
		return response, nil
	}
}

func cloneURL(value *url.URL) *url.URL {
	copy := *value
	return &copy
}

func validateFirebaseSSERedirect(target *url.URL, expectedPath, expectedQuery string) error {
	if target == nil || !strings.EqualFold(target.Scheme, "https") || target.User != nil || target.Fragment != "" || target.Hostname() == "" || target.Port() != "" && target.Port() != "443" || !isOfficialFirebaseDatabaseHost(target.Hostname()) {
		return fmt.Errorf("Google Cloud Firebase SSE refused a redirect outside official database endpoints")
	}
	if target.EscapedPath() != expectedPath || target.Query().Encode() != expectedQuery {
		return fmt.Errorf("Google Cloud Firebase SSE refused a redirect that changed the database path or query")
	}
	return nil
}

type firebaseSSEEvent struct {
	name      string
	dataLines [][]byte
	seenEvent bool
}

func readFirebaseSSE(ctx context.Context, source io.Reader, plan gcpFirebaseSSEPlan, sink *cloudWebSocketOutputSink) (int, error) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), gcpFirebaseMaxEventBytes+1)
	event := firebaseSSEEvent{}
	written := 0
	dispatch := func() error {
		if !event.seenEvent && len(event.dataLines) == 0 {
			event = firebaseSSEEvent{}
			return nil
		}
		eventName := event.name
		encoded, emit, err := canonicalFirebaseSSEEvent(event)
		event = firebaseSSEEvent{}
		if err != nil {
			return err
		}
		if !emit || !plan.IncludeKeepAlive && eventName == "keep-alive" {
			return nil
		}
		if err := sink.writeMessage(encoded); err != nil {
			return err
		}
		written++
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			if err := dispatch(); err != nil {
				return written, err
			}
			if written >= plan.MaxEvents {
				return written, nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found {
			return written, fmt.Errorf("Google Cloud Firebase SSE returned an invalid event field")
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch string(field) {
		case "event":
			if event.seenEvent || len(value) == 0 || len(value) > 64 || !utf8.Valid(value) {
				return written, fmt.Errorf("Google Cloud Firebase SSE returned an invalid event name")
			}
			event.name = string(value)
			event.seenEvent = true
		case "data":
			event.dataLines = append(event.dataLines, append([]byte(nil), value...))
			if firebaseSSEDataSize(event.dataLines) > gcpFirebaseMaxEventBytes {
				return written, fmt.Errorf("Google Cloud Firebase SSE event exceeds %d bytes", gcpFirebaseMaxEventBytes)
			}
		default:
			return written, fmt.Errorf("Google Cloud Firebase SSE returned an unsupported event field")
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		return written, fmt.Errorf("read Google Cloud Firebase SSE event stream")
	}
	if event.seenEvent || len(event.dataLines) > 0 {
		if err := dispatch(); err != nil {
			return written, err
		}
	}
	if written >= plan.MaxEvents {
		return written, nil
	}
	return written, fmt.Errorf("Google Cloud Firebase SSE ended before the requested event limit")
}

func firebaseSSEDataSize(lines [][]byte) int {
	total := 0
	for _, line := range lines {
		total += len(line) + 1
	}
	return total
}

func canonicalFirebaseSSEEvent(event firebaseSSEEvent) ([]byte, bool, error) {
	data := bytes.Join(event.dataLines, []byte{'\n'})
	switch event.name {
	case "keep-alive":
		if string(bytes.TrimSpace(data)) != "null" {
			return nil, false, fmt.Errorf("Google Cloud Firebase SSE returned an invalid keep-alive event")
		}
		return []byte(`{"data":null,"event":"keep-alive"}`), true, nil
	case "cancel":
		return nil, false, fmt.Errorf("Google Cloud Firebase SSE read was cancelled by database security rules")
	case "auth_revoked":
		return nil, false, fmt.Errorf("Google Cloud Firebase SSE credential was revoked or expired")
	case "put", "patch":
	default:
		return nil, false, fmt.Errorf("Google Cloud Firebase SSE returned an unsupported event type")
	}
	var envelope map[string]json.RawMessage
	if len(data) == 0 || len(data) > gcpFirebaseMaxEventBytes || json.Unmarshal(data, &envelope) != nil || len(envelope) != 2 {
		return nil, false, fmt.Errorf("Google Cloud Firebase SSE returned an invalid data event")
	}
	rawPath, hasPath := envelope["path"]
	rawData, hasData := envelope["data"]
	var path string
	if !hasPath || !hasData || json.Unmarshal(rawPath, &path) != nil || !validFirebaseEventPath(path) || len(rawData) == 0 || !json.Valid(rawData) {
		return nil, false, fmt.Errorf("Google Cloud Firebase SSE returned an invalid data event")
	}
	var value any
	if json.Unmarshal(rawData, &value) != nil || firebaseContainsCredentialField(value) || firebaseCredentialPath(path) {
		return nil, false, fmt.Errorf("Google Cloud Firebase SSE refused credential-bearing provider data")
	}
	if event.name == "patch" {
		if _, ok := value.(map[string]any); !ok {
			return nil, false, fmt.Errorf("Google Cloud Firebase SSE patch data must be an object")
		}
	}
	encoded, err := json.Marshal(map[string]any{"event": event.name, "path": path, "data": value})
	if err != nil {
		return nil, false, fmt.Errorf("encode Google Cloud Firebase SSE event")
	}
	return encoded, true, nil
}

func validFirebaseEventPath(path string) bool {
	if path == "" || path[0] != '/' || len(path) > 4096 || !utf8.ValidString(path) {
		return false
	}
	for _, character := range path {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func firebaseCredentialPath(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		switch normalizedOperation(segment) {
		case "auth", "authorization", "apikey", "token", "idtoken", "refreshtoken", "accesstoken", "bearertoken", "clientsecret", "credential", "sessiontoken", "password", "secret", "privatekey":
			return true
		}
	}
	return false
}

func firebaseContainsCredentialField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			if firebaseCredentialPath("/"+name) || firebaseContainsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if firebaseContainsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

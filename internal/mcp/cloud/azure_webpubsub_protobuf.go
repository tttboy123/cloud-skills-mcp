package cloud

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

const azureWebPubSubMaxProtobufFields = 256

type azureWebPubSubProtobufField struct {
	number protowire.Number
	wire   protowire.Type
	bytes  []byte
	varint uint64
}

func encodeAzureWebPubSubProtobufClientMessage(raw json.RawMessage) ([]byte, uint64, error) {
	if len(raw) == 0 || len(raw) > maxRequestPayloadBytes || !json.Valid(raw) {
		return nil, 0, fmt.Errorf("protobuf client message must be bounded JSON")
	}
	var message map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&message) != nil || ensureJSONDecoderEOF(decoder) != nil || azureRealtimeContainsCredentialField(message) {
		return nil, 0, fmt.Errorf("protobuf client message is invalid or contains credentials")
	}
	kind, ok := message["type"].(string)
	if !ok {
		return nil, 0, fmt.Errorf("protobuf client message requires type")
	}
	ackID, _, err := azureWebPubSubProtobufUint(message, "ackId", math.MaxUint64)
	if err != nil {
		return nil, 0, err
	}
	var outerField protowire.Number
	var payload []byte
	switch kind {
	case "joinGroup", "leaveGroup":
		if err := azureWebPubSubProtobufOnly(message, "type", "group", "ackId"); err != nil {
			return nil, 0, err
		}
		group, err := azureWebPubSubProtobufString(message, "group", 256, true)
		if err != nil {
			return nil, 0, err
		}
		payload = azureWebPubSubProtobufAppendString(payload, 1, group)
		if ackID != 0 {
			payload = azureWebPubSubProtobufAppendVarint(payload, 2, ackID)
		}
		if kind == "joinGroup" {
			outerField = 6
		} else {
			outerField = 7
		}
	case "sendToGroup":
		if err := azureWebPubSubProtobufOnly(message, "type", "group", "ackId", "noEcho", "dataType", "data", "stream"); err != nil {
			return nil, 0, err
		}
		group, err := azureWebPubSubProtobufString(message, "group", 256, true)
		if err != nil {
			return nil, 0, err
		}
		payload = azureWebPubSubProtobufAppendString(payload, 1, group)
		if ackID != 0 {
			payload = azureWebPubSubProtobufAppendVarint(payload, 2, ackID)
		}
		data, hasData, err := azureWebPubSubProtobufEncodeData(message)
		if err != nil {
			return nil, 0, err
		}
		if hasData {
			payload = azureWebPubSubProtobufAppendBytes(payload, 3, data)
		}
		if rawNoEcho, exists := message["noEcho"]; exists {
			noEcho, ok := rawNoEcho.(bool)
			if !ok {
				return nil, 0, fmt.Errorf("noEcho must be boolean")
			}
			payload = azureWebPubSubProtobufAppendVarint(payload, 4, uint64FromBool(noEcho))
		}
		if rawStream, exists := message["stream"]; exists {
			if hasData || ackID != 0 {
				return nil, 0, fmt.Errorf("stream start cannot contain data or ackId")
			}
			stream, ok := rawStream.(map[string]any)
			if !ok || azureRealtimeContainsCredentialField(stream) {
				return nil, 0, fmt.Errorf("stream start is invalid")
			}
			if err := azureWebPubSubProtobufOnly(stream, "streamId", "idleTimeoutMs"); err != nil {
				return nil, 0, err
			}
			streamID, err := azureWebPubSubProtobufString(stream, "streamId", 256, true)
			if err != nil {
				return nil, 0, err
			}
			streamPayload := azureWebPubSubProtobufAppendString(nil, 1, streamID)
			idleTimeout, present, err := azureWebPubSubProtobufUint(stream, "idleTimeoutMs", math.MaxUint32)
			if err != nil || present && idleTimeout == 0 {
				return nil, 0, fmt.Errorf("idleTimeoutMs must be a positive uint32")
			}
			if present {
				streamPayload = azureWebPubSubProtobufAppendVarint(streamPayload, 2, idleTimeout)
			}
			payload = azureWebPubSubProtobufAppendBytes(payload, 7, streamPayload)
		} else if !hasData {
			return nil, 0, fmt.Errorf("sendToGroup requires data or stream")
		}
		outerField = 1
	case "event":
		if err := azureWebPubSubProtobufOnly(message, "type", "event", "ackId", "dataType", "data"); err != nil {
			return nil, 0, err
		}
		event, err := azureWebPubSubProtobufString(message, "event", 128, true)
		if err != nil {
			return nil, 0, err
		}
		data, hasData, err := azureWebPubSubProtobufEncodeData(message)
		if err != nil || !hasData {
			return nil, 0, fmt.Errorf("event requires protobuf, text, or binary data")
		}
		payload = azureWebPubSubProtobufAppendString(payload, 1, event)
		payload = azureWebPubSubProtobufAppendBytes(payload, 2, data)
		if ackID != 0 {
			payload = azureWebPubSubProtobufAppendVarint(payload, 3, ackID)
		}
		outerField = 5
	case "ping":
		if err := azureWebPubSubProtobufOnly(message, "type"); err != nil {
			return nil, 0, err
		}
		outerField = 9
	case "streamData":
		if err := azureWebPubSubProtobufOnly(message, "type", "streamId", "streamSequenceId", "dataType", "data"); err != nil {
			return nil, 0, err
		}
		streamID, err := azureWebPubSubProtobufString(message, "streamId", 256, true)
		if err != nil {
			return nil, 0, err
		}
		payload = azureWebPubSubProtobufAppendString(payload, 1, streamID)
		sequence, sequencePresent, err := azureWebPubSubProtobufUint(message, "streamSequenceId", math.MaxUint64)
		if err != nil || sequencePresent && sequence == 0 {
			return nil, 0, fmt.Errorf("streamSequenceId must be a positive uint64")
		}
		data, hasData, err := azureWebPubSubProtobufEncodeData(message)
		if err != nil || sequencePresent != hasData {
			return nil, 0, fmt.Errorf("stream data requires both streamSequenceId and data, or neither for keepalive")
		}
		if sequencePresent {
			payload = azureWebPubSubProtobufAppendVarint(payload, 2, sequence)
			payload = azureWebPubSubProtobufAppendBytes(payload, 3, data)
		}
		outerField = 13
	case "streamEnd":
		if err := azureWebPubSubProtobufOnly(message, "type", "streamId", "error"); err != nil {
			return nil, 0, err
		}
		streamID, err := azureWebPubSubProtobufString(message, "streamId", 256, true)
		if err != nil {
			return nil, 0, err
		}
		payload = azureWebPubSubProtobufAppendString(payload, 1, streamID)
		if rawError, exists := message["error"]; exists {
			failure, ok := rawError.(map[string]any)
			if !ok || azureRealtimeContainsCredentialField(failure) {
				return nil, 0, fmt.Errorf("stream end error is invalid")
			}
			if err := azureWebPubSubProtobufOnly(failure, "message", "userErrorCode"); err != nil {
				return nil, 0, err
			}
			failurePayload := []byte(nil)
			if value, exists := failure["message"]; exists {
				text, ok := value.(string)
				if !ok || !azureWebPubSubBoundedValue(text, 1024) {
					return nil, 0, fmt.Errorf("stream end error message is invalid")
				}
				failurePayload = azureWebPubSubProtobufAppendString(failurePayload, 1, text)
			}
			if value, exists := failure["userErrorCode"]; exists {
				code, ok := value.(string)
				if !ok || !azureWebPubSubBoundedValue(code, 256) {
					return nil, 0, fmt.Errorf("stream end userErrorCode is invalid")
				}
				failurePayload = azureWebPubSubProtobufAppendString(failurePayload, 2, code)
			}
			if len(failurePayload) == 0 {
				return nil, 0, fmt.Errorf("stream end error cannot be empty")
			}
			payload = azureWebPubSubProtobufAppendBytes(payload, 2, failurePayload)
		}
		outerField = 14
	default:
		return nil, 0, fmt.Errorf("unsupported protobuf client message type")
	}
	return azureWebPubSubProtobufAppendBytes(nil, outerField, payload), ackID, nil
}

func azureWebPubSubProtobufEncodeData(message map[string]any) ([]byte, bool, error) {
	rawType, typePresent := message["dataType"]
	rawData, dataPresent := message["data"]
	if !typePresent && !dataPresent {
		return nil, false, nil
	}
	dataType, ok := rawType.(string)
	if !ok || !dataPresent {
		return nil, false, fmt.Errorf("dataType and data must be provided together")
	}
	switch dataType {
	case "text":
		text, ok := rawData.(string)
		if !ok || len(text) > maxRequestPayloadBytes || !utf8.ValidString(text) {
			return nil, false, fmt.Errorf("text data is invalid")
		}
		return azureWebPubSubProtobufAppendString(nil, 1, text), true, nil
	case "binary":
		encoded, ok := rawData.(string)
		if !ok {
			return nil, false, fmt.Errorf("binary data must be base64")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(decoded) > maxRequestPayloadBytes {
			return nil, false, fmt.Errorf("binary data must be bounded canonical base64")
		}
		return azureWebPubSubProtobufAppendBytes(nil, 2, decoded), true, nil
	case "protobuf":
		value, ok := rawData.(map[string]any)
		if !ok || azureRealtimeContainsCredentialField(value) {
			return nil, false, fmt.Errorf("protobuf data must be an Any object")
		}
		if err := azureWebPubSubProtobufOnly(value, "typeUrl", "value"); err != nil {
			return nil, false, err
		}
		typeURL, err := azureWebPubSubProtobufString(value, "typeUrl", 2048, true)
		if err != nil || !azureWebPubSubProtobufTypeURL(typeURL) {
			return nil, false, fmt.Errorf("protobuf Any typeUrl is invalid")
		}
		encoded, ok := value["value"].(string)
		if !ok {
			return nil, false, fmt.Errorf("protobuf Any value must be base64")
		}
		decoded, decodeErr := base64.StdEncoding.Strict().DecodeString(encoded)
		if decodeErr != nil || len(decoded) > maxRequestPayloadBytes {
			return nil, false, fmt.Errorf("protobuf Any value must be bounded canonical base64")
		}
		anyPayload := azureWebPubSubProtobufAppendString(nil, 1, typeURL)
		if len(decoded) > 0 {
			anyPayload = azureWebPubSubProtobufAppendBytes(anyPayload, 2, decoded)
		}
		return azureWebPubSubProtobufAppendBytes(nil, 3, anyPayload), true, nil
	default:
		return nil, false, fmt.Errorf("protobuf protocol dataType must be text, binary, or protobuf")
	}
}

func parseAzureWebPubSubProtobufServerMessage(messageType cloudWebSocketMessageType, data []byte, reliable bool) ([]byte, string, uint64, uint64, bool, error) {
	if messageType != cloudWebSocketMessageBinary || len(data) == 0 || len(data) > maxRequestPayloadBytes {
		return nil, "", 0, 0, false, fmt.Errorf("Azure Web PubSub returned invalid protobuf binary")
	}
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return nil, "", 0, 0, false, fmt.Errorf("Azure Web PubSub returned invalid protobuf binary")
	}
	var recognized *azureWebPubSubProtobufField
	for index := range fields {
		field := &fields[index]
		if field.number == 1 || field.number == 2 || field.number == 3 || field.number == 4 || field.number == 6 || field.number == 7 || field.number == 8 {
			if field.wire != protowire.BytesType || recognized != nil {
				return nil, "", 0, 0, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf oneof")
			}
			recognized = field
		}
	}
	if recognized == nil {
		return nil, "", 0, 0, false, fmt.Errorf("Azure Web PubSub protobuf response has no message")
	}
	message := map[string]any{}
	var kind string
	var sequenceID, ackID uint64
	terminal := false
	switch recognized.number {
	case 1:
		kind = "ack"
		ackID, message, err = azureWebPubSubProtobufDecodeAck(recognized.bytes, reliable)
	case 2:
		kind = "message"
		sequenceID, message, err = azureWebPubSubProtobufDecodeDataMessage(recognized.bytes, reliable)
	case 3:
		kind = "system"
		message, terminal, err = azureWebPubSubProtobufDecodeSystem(recognized.bytes, reliable)
	case 4:
		kind = "pong"
		message = map[string]any{"type": kind}
	case 6:
		kind = "streamAck"
		message, err = azureWebPubSubProtobufDecodeStreamAck(recognized.bytes)
	case 7:
		return nil, "", 0, 0, false, fmt.Errorf("Azure Web PubSub returned a stream rejection")
	case 8:
		kind = "streamClosed"
		message, err = azureWebPubSubProtobufDecodeStreamClosed(recognized.bytes)
	}
	if err != nil {
		return nil, "", 0, 0, false, err
	}
	canonical, err := json.Marshal(message)
	if err != nil {
		return nil, "", 0, 0, false, fmt.Errorf("encode Azure Web PubSub protobuf response")
	}
	return canonical, kind, sequenceID, ackID, terminal, nil
}

func azureWebPubSubProtobufDecodeAck(data []byte, reliable bool) (uint64, map[string]any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf acknowledgement")
	}
	ackID, present, err := azureWebPubSubProtobufOneVarint(fields, 1)
	if err != nil || !present || ackID == 0 {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf acknowledgement")
	}
	successValue, successPresent, err := azureWebPubSubProtobufOneVarint(fields, 2)
	if err != nil || successValue > 1 {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf acknowledgement")
	}
	success := successPresent && successValue == 1
	message := map[string]any{"type": "ack", "ackId": ackID, "success": success}
	errorPayload, errorPresent, errorErr := azureWebPubSubProtobufOneBytes(fields, 3)
	if errorErr != nil {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf acknowledgement")
	}
	duplicate := false
	if errorPresent {
		failure, decodeErr := azureWebPubSubProtobufDecodeError(errorPayload)
		if decodeErr != nil {
			return 0, nil, decodeErr
		}
		message["error"] = failure
		duplicate = failure["name"] == "Duplicate"
	}
	if !success && !(reliable && duplicate) {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned a failed acknowledgement")
	}
	return ackID, message, nil
}

func azureWebPubSubProtobufDecodeDataMessage(data []byte, reliable bool) (uint64, map[string]any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf data message")
	}
	from, present, err := azureWebPubSubProtobufOneString(fields, 1)
	if err != nil || !present || from != "group" && from != "server" {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf data message")
	}
	message := map[string]any{"type": "message", "from": from}
	if group, exists, groupErr := azureWebPubSubProtobufOneString(fields, 2); groupErr != nil {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf data message")
	} else if exists {
		if from != "group" || !azureWebPubSubBoundedValue(group, 256) {
			return 0, nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf data message")
		}
		message["group"] = group
	} else if from == "group" {
		return 0, nil, fmt.Errorf("Azure Web PubSub group message omitted group")
	}
	dataPayload, dataPresent, dataErr := azureWebPubSubProtobufOneBytes(fields, 3)
	if dataErr != nil || !dataPresent {
		return 0, nil, fmt.Errorf("Azure Web PubSub protobuf data message omitted data")
	}
	dataType, decoded, decodeErr := azureWebPubSubProtobufDecodeMessageData(dataPayload)
	if decodeErr != nil {
		return 0, nil, decodeErr
	}
	message["dataType"], message["data"] = dataType, decoded
	sequenceID, sequencePresent, sequenceErr := azureWebPubSubProtobufOneVarint(fields, 4)
	if sequenceErr != nil || reliable && (!sequencePresent || sequenceID == 0) {
		return 0, nil, fmt.Errorf("Azure reliable Web PubSub protobuf message omitted sequenceId")
	}
	if sequencePresent {
		message["sequenceId"] = sequenceID
	}
	if streamPayload, exists, streamErr := azureWebPubSubProtobufOneBytes(fields, 6); streamErr != nil {
		return 0, nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf stream information")
	} else if exists {
		stream, decodeErr := azureWebPubSubProtobufDecodeStreamInfo(streamPayload)
		if decodeErr != nil {
			return 0, nil, decodeErr
		}
		message["stream"] = stream
	}
	return sequenceID, message, nil
}

func azureWebPubSubProtobufDecodeSystem(data []byte, reliable bool) (map[string]any, bool, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf system message")
	}
	connected, hasConnected, err := azureWebPubSubProtobufOneBytes(fields, 1)
	if err != nil {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf system message")
	}
	disconnected, hasDisconnected, err := azureWebPubSubProtobufOneBytes(fields, 2)
	if err != nil || hasConnected == hasDisconnected {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf system message")
	}
	if hasDisconnected {
		disconnectedFields, parseErr := azureWebPubSubProtobufParseFields(disconnected)
		if parseErr != nil {
			return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf disconnected event")
		}
		reason, _, reasonErr := azureWebPubSubProtobufOneString(disconnectedFields, 2)
		if reasonErr != nil || len(reason) > 4096 || !utf8.ValidString(reason) {
			return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf disconnected event")
		}
		return map[string]any{"type": "system", "event": "disconnected", "reason": reason}, true, nil
	}
	connectedFields, parseErr := azureWebPubSubProtobufParseFields(connected)
	if parseErr != nil {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf connected event")
	}
	connectionID, present, idErr := azureWebPubSubProtobufOneString(connectedFields, 1)
	if idErr != nil || !present || !azureWebPubSubBoundedValue(connectionID, 512) {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf connected event")
	}
	userID, _, userErr := azureWebPubSubProtobufOneString(connectedFields, 2)
	if userErr != nil || len(userID) > 128 || !utf8.ValidString(userID) {
		return nil, false, fmt.Errorf("Azure Web PubSub returned an invalid protobuf connected event")
	}
	token, tokenPresent, tokenErr := azureWebPubSubProtobufOneString(connectedFields, 3)
	if tokenErr != nil || reliable && (!tokenPresent || !azureWebPubSubBoundedValue(token, 16384)) {
		return nil, false, fmt.Errorf("Azure reliable Web PubSub returned invalid recovery state")
	}
	message := map[string]any{"type": "system", "event": "connected", "connectionId": connectionID, "userId": userID}
	if tokenPresent {
		message["reconnectionToken"] = token
	}
	return message, false, nil
}

func azureWebPubSubProtobufDecodeStreamAck(data []byte) (map[string]any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf stream acknowledgement")
	}
	streamID, present, idErr := azureWebPubSubProtobufOneString(fields, 1)
	expected, sequencePresent, sequenceErr := azureWebPubSubProtobufOneVarint(fields, 2)
	if idErr != nil || sequenceErr != nil || !present || !sequencePresent || expected == 0 || !azureWebPubSubBoundedValue(streamID, 256) {
		return nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf stream acknowledgement")
	}
	return map[string]any{"type": "streamAck", "streamId": streamID, "expectedSequenceId": expected}, nil
}

func azureWebPubSubProtobufDecodeStreamClosed(data []byte) (map[string]any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf stream close")
	}
	streamID, present, idErr := azureWebPubSubProtobufOneString(fields, 1)
	if idErr != nil || !present || !azureWebPubSubBoundedValue(streamID, 256) {
		return nil, fmt.Errorf("Azure Web PubSub returned an invalid protobuf stream close")
	}
	if _, errorPresent, errorErr := azureWebPubSubProtobufOneBytes(fields, 2); errorErr != nil || errorPresent {
		return nil, fmt.Errorf("Azure Web PubSub stream closed with an error")
	}
	return map[string]any{"type": "streamClosed", "streamId": streamID}, nil
}

func azureWebPubSubProtobufDecodeStreamInfo(data []byte) (map[string]any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf stream information")
	}
	streamID, present, idErr := azureWebPubSubProtobufOneString(fields, 1)
	sequence, sequencePresent, sequenceErr := azureWebPubSubProtobufOneVarint(fields, 2)
	if idErr != nil || sequenceErr != nil || !present || !sequencePresent || sequence == 0 || !azureWebPubSubBoundedValue(streamID, 256) {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf stream information")
	}
	stream := map[string]any{"streamId": streamID, "streamSequenceId": sequence}
	if end, endPresent, endErr := azureWebPubSubProtobufOneVarint(fields, 3); endErr != nil || end > 1 {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf stream information")
	} else if endPresent {
		stream["endOfStream"] = end == 1
	}
	if errorPayload, errorPresent, errorErr := azureWebPubSubProtobufOneBytes(fields, 4); errorErr != nil {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf stream information")
	} else if errorPresent {
		failure, decodeErr := azureWebPubSubProtobufDecodeStreamError(errorPayload)
		if decodeErr != nil {
			return nil, decodeErr
		}
		stream["error"] = failure
	}
	return stream, nil
}

func azureWebPubSubProtobufDecodeMessageData(data []byte) (string, any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return "", nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf message data")
	}
	var selected *azureWebPubSubProtobufField
	for index := range fields {
		field := &fields[index]
		if field.number >= 1 && field.number <= 4 {
			if field.wire != protowire.BytesType || selected != nil {
				return "", nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf message data")
			}
			selected = field
		}
	}
	if selected == nil {
		return "", nil, fmt.Errorf("Azure Web PubSub returned empty protobuf message data")
	}
	switch selected.number {
	case 1:
		if !utf8.Valid(selected.bytes) {
			return "", nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf text data")
		}
		return "text", string(selected.bytes), nil
	case 2:
		return "binary", base64.StdEncoding.EncodeToString(selected.bytes), nil
	case 3:
		fields, parseErr := azureWebPubSubProtobufParseFields(selected.bytes)
		if parseErr != nil {
			return "", nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf Any data")
		}
		typeURL, present, typeErr := azureWebPubSubProtobufOneString(fields, 1)
		value, _, valueErr := azureWebPubSubProtobufOneBytes(fields, 2)
		if typeErr != nil || valueErr != nil || !present || !azureWebPubSubProtobufTypeURL(typeURL) {
			return "", nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf Any data")
		}
		return "protobuf", map[string]any{"typeUrl": typeURL, "value": base64.StdEncoding.EncodeToString(value)}, nil
	case 4:
		if !utf8.Valid(selected.bytes) || !json.Valid(selected.bytes) {
			return "", nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf JSON data")
		}
		return "json", json.RawMessage(append([]byte(nil), selected.bytes...)), nil
	}
	return "", nil, fmt.Errorf("Azure Web PubSub returned unsupported protobuf message data")
}

func azureWebPubSubProtobufDecodeError(data []byte) (map[string]any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf error")
	}
	name, present, nameErr := azureWebPubSubProtobufOneString(fields, 1)
	message, _, messageErr := azureWebPubSubProtobufOneString(fields, 2)
	if nameErr != nil || messageErr != nil || !present || !azureWebPubSubBoundedValue(name, 256) || len(message) > 4096 || !utf8.ValidString(message) {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf error")
	}
	return map[string]any{"name": name, "message": message}, nil
}

func azureWebPubSubProtobufDecodeStreamError(data []byte) (map[string]any, error) {
	fields, err := azureWebPubSubProtobufParseFields(data)
	if err != nil {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf stream error")
	}
	name, present, nameErr := azureWebPubSubProtobufOneString(fields, 1)
	message, _, messageErr := azureWebPubSubProtobufOneString(fields, 2)
	code, codePresent, codeErr := azureWebPubSubProtobufOneString(fields, 3)
	if nameErr != nil || messageErr != nil || codeErr != nil || !present || !azureWebPubSubBoundedValue(name, 256) || len(message) > 4096 || !utf8.ValidString(message) || codePresent && !azureWebPubSubBoundedValue(code, 256) {
		return nil, fmt.Errorf("Azure Web PubSub returned invalid protobuf stream error")
	}
	failure := map[string]any{"name": name, "message": message}
	if codePresent {
		failure["userErrorCode"] = code
	}
	return failure, nil
}

func azureWebPubSubProtobufParseFields(data []byte) ([]azureWebPubSubProtobufField, error) {
	fields := make([]azureWebPubSubProtobufField, 0, 8)
	for len(data) > 0 {
		number, wire, consumed := protowire.ConsumeTag(data)
		if consumed < 0 || number <= 0 {
			return nil, fmt.Errorf("invalid protobuf tag")
		}
		data = data[consumed:]
		field := azureWebPubSubProtobufField{number: number, wire: wire}
		switch wire {
		case protowire.VarintType:
			value, count := protowire.ConsumeVarint(data)
			if count < 0 {
				return nil, fmt.Errorf("invalid protobuf varint")
			}
			field.varint = value
			data = data[count:]
		case protowire.BytesType:
			value, count := protowire.ConsumeBytes(data)
			if count < 0 {
				return nil, fmt.Errorf("invalid protobuf bytes")
			}
			field.bytes = value
			data = data[count:]
		case protowire.Fixed32Type:
			_, count := protowire.ConsumeFixed32(data)
			if count < 0 {
				return nil, fmt.Errorf("invalid protobuf fixed32")
			}
			data = data[count:]
		case protowire.Fixed64Type:
			_, count := protowire.ConsumeFixed64(data)
			if count < 0 {
				return nil, fmt.Errorf("invalid protobuf fixed64")
			}
			data = data[count:]
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type")
		}
		fields = append(fields, field)
		if len(fields) > azureWebPubSubMaxProtobufFields {
			return nil, fmt.Errorf("too many protobuf fields")
		}
	}
	return fields, nil
}

func azureWebPubSubProtobufOneBytes(fields []azureWebPubSubProtobufField, number protowire.Number) ([]byte, bool, error) {
	var value []byte
	present := false
	for _, field := range fields {
		if field.number != number {
			continue
		}
		if present || field.wire != protowire.BytesType {
			return nil, false, fmt.Errorf("invalid protobuf bytes field")
		}
		value, present = field.bytes, true
	}
	return value, present, nil
}

func azureWebPubSubProtobufOneString(fields []azureWebPubSubProtobufField, number protowire.Number) (string, bool, error) {
	value, present, err := azureWebPubSubProtobufOneBytes(fields, number)
	if err != nil || present && !utf8.Valid(value) {
		return "", false, fmt.Errorf("invalid protobuf string field")
	}
	return string(value), present, nil
}

func azureWebPubSubProtobufOneVarint(fields []azureWebPubSubProtobufField, number protowire.Number) (uint64, bool, error) {
	var value uint64
	present := false
	for _, field := range fields {
		if field.number != number {
			continue
		}
		if present || field.wire != protowire.VarintType {
			return 0, false, fmt.Errorf("invalid protobuf varint field")
		}
		value, present = field.varint, true
	}
	return value, present, nil
}

func azureWebPubSubProtobufAppendBytes(target []byte, number protowire.Number, value []byte) []byte {
	target = protowire.AppendTag(target, number, protowire.BytesType)
	return protowire.AppendBytes(target, value)
}

func azureWebPubSubProtobufAppendString(target []byte, number protowire.Number, value string) []byte {
	return azureWebPubSubProtobufAppendBytes(target, number, []byte(value))
}

func azureWebPubSubProtobufAppendVarint(target []byte, number protowire.Number, value uint64) []byte {
	target = protowire.AppendTag(target, number, protowire.VarintType)
	return protowire.AppendVarint(target, value)
}

func azureWebPubSubProtobufOnly(message map[string]any, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key := range message {
		if !set[key] {
			return fmt.Errorf("unsupported protobuf message field %q", key)
		}
	}
	return nil
}

func azureWebPubSubProtobufTypeURL(value string) bool {
	separator := strings.LastIndexByte(value, '/')
	return azureWebPubSubBoundedValue(value, 2048) && separator > 0 && separator < len(value)-1 && !strings.ContainsAny(value, " \t")
}

func azureWebPubSubProtobufString(message map[string]any, key string, maximum int, required bool) (string, error) {
	raw, exists := message[key]
	if !exists {
		if required {
			return "", fmt.Errorf("%s is required", key)
		}
		return "", nil
	}
	value, ok := raw.(string)
	if !ok || !azureWebPubSubBoundedValue(value, maximum) {
		return "", fmt.Errorf("%s is invalid", key)
	}
	return value, nil
}

func azureWebPubSubProtobufUint(message map[string]any, key string, maximum uint64) (uint64, bool, error) {
	raw, exists := message[key]
	if !exists {
		return 0, false, nil
	}
	number, ok := raw.(json.Number)
	if !ok {
		return 0, false, fmt.Errorf("%s must be an integer", key)
	}
	value, err := number.Int64()
	if err == nil {
		if value <= 0 || uint64(value) > maximum {
			return 0, false, fmt.Errorf("%s must be a positive bounded integer", key)
		}
		return uint64(value), true, nil
	}
	parsed, parseErr := parseAzureWebPubSubUint64(string(number))
	if parseErr != nil || parsed == 0 || parsed > maximum {
		return 0, false, fmt.Errorf("%s must be a positive bounded integer", key)
	}
	return parsed, true, nil
}

func parseAzureWebPubSubUint64(value string) (uint64, error) {
	var parsed uint64
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("not uint64")
		}
		digit := uint64(character - '0')
		if parsed > (math.MaxUint64-digit)/10 {
			return 0, fmt.Errorf("uint64 overflow")
		}
		parsed = parsed*10 + digit
	}
	if value == "" {
		return 0, fmt.Errorf("empty uint64")
	}
	return parsed, nil
}

func uint64FromBool(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

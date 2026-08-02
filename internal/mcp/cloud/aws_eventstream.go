package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/eventstream"
)

const (
	awsPayloadModeEventStream       = "aws-eventstream"
	awsSigV4StreamingEventsPayload  = "STREAMING-AWS4-HMAC-SHA256-EVENTS"
	maxAWSEventStreamFrameBytes     = 24 * 1024 * 1024
	maxAWSEventStreamHeadersBytes   = 128 * 1024
	awsEventStreamMinimumFrameBytes = 16
)

func configureAWSEventStreamRequest(request *http.Request) error {
	if request == nil || request.Body == nil {
		return fmt.Errorf("AWS SigV4 aws-eventstream requires an encoded request body")
	}
	request.Header.Set("Content-Type", "application/vnd.amazon.eventstream")
	request.Header.Set("X-Amz-Content-Sha256", awsSigV4StreamingEventsPayload)
	request.Header.Del("Content-Length")
	request.ContentLength = -1
	request.GetBody = nil
	return nil
}

func newAWSSigV4EventStreamReader(ctx context.Context, source io.ReadCloser, credentials aws.Credentials, service, region string, signingTime func() time.Time, seed []byte) io.ReadCloser {
	return &awsSigV4EventStreamReader{
		ctx: ctx, source: bufio.NewReader(source), sourceCloser: source,
		signer: awsv4.NewStreamSigner(credentials, service, region, seed), now: signingTime,
		decoder: eventstream.NewDecoder(), encoder: eventstream.NewEncoder(),
	}
}

type awsSigV4EventStreamReader struct {
	ctx          context.Context
	source       *bufio.Reader
	sourceCloser io.Closer
	signer       *awsv4.StreamSigner
	now          func() time.Time
	decoder      *eventstream.Decoder
	encoder      *eventstream.Encoder
	pending      []byte
	done         bool
}

func (reader *awsSigV4EventStreamReader) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	if len(reader.pending) == 0 {
		if reader.done {
			return 0, io.EOF
		}
		if err := reader.loadNextFrame(); err != nil {
			return 0, err
		}
	}
	written := copy(target, reader.pending)
	reader.pending = reader.pending[written:]
	return written, nil
}

func (reader *awsSigV4EventStreamReader) Close() error {
	reader.done = true
	reader.pending = nil
	return reader.sourceCloser.Close()
}

func (reader *awsSigV4EventStreamReader) loadNextFrame() error {
	if _, err := reader.source.Peek(1); err != nil {
		if err == io.EOF {
			reader.done = true
			return reader.signAndEncode(nil)
		}
		return fmt.Errorf("read AWS event stream: %w", err)
	}
	prelude, err := reader.source.Peek(8)
	if err != nil {
		return fmt.Errorf("read AWS event stream prelude: %w", err)
	}
	totalLength := binary.BigEndian.Uint32(prelude[:4])
	headersLength := binary.BigEndian.Uint32(prelude[4:8])
	if totalLength < awsEventStreamMinimumFrameBytes || totalLength > maxAWSEventStreamFrameBytes || headersLength > maxAWSEventStreamHeadersBytes || headersLength > totalLength-awsEventStreamMinimumFrameBytes {
		return fmt.Errorf("AWS event stream frame length is outside the bounded protocol limits")
	}
	message, err := reader.decoder.Decode(io.LimitReader(reader.source, int64(totalLength)), nil)
	if err != nil {
		return fmt.Errorf("decode AWS event stream frame: %w", err)
	}
	var encoded bytes.Buffer
	if err := reader.encoder.Encode(&encoded, message); err != nil {
		return fmt.Errorf("re-encode AWS event stream frame: %w", err)
	}
	return reader.signAndEncode(encoded.Bytes())
}

func (reader *awsSigV4EventStreamReader) signAndEncode(frame []byte) error {
	now := reader.now().UTC()
	message := eventstream.Message{Payload: frame}
	message.Headers.Set(eventstream.DateHeader, eventstream.TimestampValue(now))
	var headers bytes.Buffer
	if err := eventstream.EncodeHeaders(&headers, message.Headers); err != nil {
		return fmt.Errorf("encode AWS event stream signing headers: %w", err)
	}
	signature, err := reader.signer.GetSignature(reader.ctx, headers.Bytes(), frame, now)
	if err != nil {
		return fmt.Errorf("sign AWS event stream frame: %w", err)
	}
	message.Headers.Set(eventstream.ChunkSignatureHeader, eventstream.BytesValue(signature))
	var encoded bytes.Buffer
	if err := reader.encoder.Encode(&encoded, message); err != nil {
		return fmt.Errorf("encode signed AWS event stream frame: %w", err)
	}
	reader.pending = encoded.Bytes()
	return nil
}

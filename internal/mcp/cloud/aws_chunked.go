package cloud

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const (
	awsPayloadModeChunked    = "aws-chunked"
	awsSigV4StreamingPayload = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	awsSigV4ChunkAlgorithm   = "AWS4-HMAC-SHA256-PAYLOAD"
	defaultAWSChunkSize      = 64 * 1024
)

func configureAWSSigV4ChunkedRequest(request *http.Request, chunkSize int) error {
	if request == nil || request.Body == nil || request.ContentLength < 0 {
		return fmt.Errorf("AWS SigV4 aws-chunked requires a body with known length")
	}
	encodedLength, err := awsChunkedEncodedLength(request.ContentLength, chunkSize)
	if err != nil {
		return err
	}
	contentEncoding := strings.TrimSpace(request.Header.Get("Content-Encoding"))
	if contentEncoding == "" {
		contentEncoding = awsPayloadModeChunked
	} else if !headerTokenContains(contentEncoding, awsPayloadModeChunked) {
		contentEncoding = awsPayloadModeChunked + "," + contentEncoding
	}
	request.Header.Set("Content-Encoding", contentEncoding)
	request.Header.Set("X-Amz-Decoded-Content-Length", strconv.FormatInt(request.ContentLength, 10))
	request.Header.Set("X-Amz-Content-Sha256", awsSigV4StreamingPayload)
	request.ContentLength = encodedLength
	request.Header.Set("Content-Length", strconv.FormatInt(encodedLength, 10))
	return nil
}

func headerTokenContains(value, expected string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), expected) {
			return true
		}
	}
	return false
}

func awsChunkedEncodedLength(decodedLength int64, chunkSize int) (int64, error) {
	if decodedLength < 0 || chunkSize < 8*1024 {
		return 0, fmt.Errorf("AWS SigV4 aws-chunked requires a non-negative length and chunk size of at least 8192 bytes")
	}
	encodedLength := int64(0)
	remaining := decodedLength
	for remaining > 0 {
		chunkLength := int64(chunkSize)
		if remaining < chunkLength {
			chunkLength = remaining
		}
		overhead := int64(len(strconv.FormatInt(chunkLength, 16)) + len(";chunk-signature=") + 64 + 4)
		if encodedLength > math.MaxInt64-chunkLength-overhead {
			return 0, fmt.Errorf("AWS SigV4 aws-chunked encoded length overflows int64")
		}
		encodedLength += chunkLength + overhead
		remaining -= chunkLength
	}
	finalOverhead := int64(1 + len(";chunk-signature=") + 64 + 4)
	if encodedLength > math.MaxInt64-finalOverhead {
		return 0, fmt.Errorf("AWS SigV4 aws-chunked encoded length overflows int64")
	}
	return encodedLength + finalOverhead, nil
}

func awsSeedSignature(authorization string) ([]byte, error) {
	const marker = "Signature="
	index := strings.LastIndex(authorization, marker)
	if index < 0 {
		return nil, fmt.Errorf("AWS SigV4 authorization is missing its seed signature")
	}
	value := strings.TrimSpace(authorization[index+len(marker):])
	if separator := strings.IndexAny(value, ", "); separator >= 0 {
		value = value[:separator]
	}
	signature, err := hex.DecodeString(value)
	if err != nil || len(signature) != sha256.Size {
		return nil, fmt.Errorf("AWS SigV4 authorization has an invalid seed signature")
	}
	return signature, nil
}

func newAWSSigV4ChunkedReader(source io.ReadCloser, decodedLength int64, credentials aws.Credentials, service, region string, signingTime time.Time, seed []byte, chunkSize int) io.ReadCloser {
	return &awsSigV4ChunkedReader{
		source: source, signingKey: deriveAWSSigV4SigningKey(credentials.SecretAccessKey, region, service, signingTime),
		amzDate:           signingTime.UTC().Format("20060102T150405Z"),
		scope:             signingTime.UTC().Format("20060102") + "/" + strings.ToLower(region) + "/" + strings.ToLower(service) + "/aws4_request",
		previousSignature: append([]byte(nil), seed...), chunkSize: chunkSize, remaining: decodedLength,
	}
}

func deriveAWSSigV4SigningKey(secretAccessKey, region, service string, signingTime time.Time) []byte {
	dateKey := hmacBytes(sha256.New, []byte("AWS4"+secretAccessKey), []byte(signingTime.UTC().Format("20060102")))
	regionKey := hmacBytes(sha256.New, dateKey, []byte(strings.ToLower(region)))
	serviceKey := hmacBytes(sha256.New, regionKey, []byte(strings.ToLower(service)))
	return hmacBytes(sha256.New, serviceKey, []byte("aws4_request"))
}

type awsSigV4ChunkedReader struct {
	source            io.ReadCloser
	signingKey        []byte
	amzDate           string
	scope             string
	previousSignature []byte
	chunkSize         int
	remaining         int64
	pending           []byte
	done              bool
}

func (reader *awsSigV4ChunkedReader) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	if len(reader.pending) == 0 {
		if reader.done {
			return 0, io.EOF
		}
		if err := reader.loadNextChunk(); err != nil {
			return 0, err
		}
	}
	written := copy(target, reader.pending)
	reader.pending = reader.pending[written:]
	return written, nil
}

func (reader *awsSigV4ChunkedReader) Close() error {
	reader.done = true
	reader.pending = nil
	return reader.source.Close()
}

func (reader *awsSigV4ChunkedReader) loadNextChunk() error {
	read := 0
	chunkLength := int64(reader.chunkSize)
	if reader.remaining < chunkLength {
		chunkLength = reader.remaining
	}
	chunk := make([]byte, int(chunkLength))
	if chunkLength > 0 {
		var err error
		read, err = io.ReadFull(reader.source, chunk)
		if err != nil {
			return fmt.Errorf("read AWS SigV4 aws-chunked body: decoded body is shorter than declared: %w", err)
		}
		reader.remaining -= int64(read)
	} else {
		probe := make([]byte, 1)
		probeRead, err := reader.source.Read(probe)
		if probeRead != 0 || (err != nil && err != io.EOF) {
			if err == nil {
				err = fmt.Errorf("extra body byte")
			}
			return fmt.Errorf("read AWS SigV4 aws-chunked body: decoded body is longer than declared: %w", err)
		}
	}
	signature := reader.signChunk(chunk)
	header := strconv.FormatInt(int64(read), 16) + ";chunk-signature=" + signature + "\r\n"
	reader.pending = make([]byte, 0, len(header)+read+2)
	reader.pending = append(reader.pending, header...)
	reader.pending = append(reader.pending, chunk...)
	reader.pending = append(reader.pending, '\r', '\n')
	if read == 0 {
		reader.done = true
	}
	return nil
}

func (reader *awsSigV4ChunkedReader) signChunk(chunk []byte) string {
	stringToSign := strings.Join([]string{
		awsSigV4ChunkAlgorithm,
		reader.amzDate,
		reader.scope,
		hex.EncodeToString(reader.previousSignature),
		sha256Hex(nil),
		sha256Hex(chunk),
	}, "\n")
	reader.previousSignature = hmacBytes(sha256.New, reader.signingKey, []byte(stringToSign))
	return hex.EncodeToString(reader.previousSignature)
}

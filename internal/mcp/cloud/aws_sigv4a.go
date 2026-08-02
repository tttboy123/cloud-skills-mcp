package cloud

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/aws/smithy-go/encoding/httpbinding"
)

const awsSigV4aAlgorithm = "AWS4-ECDSA-P256-SHA256"

// deriveAWSSigV4aKey implements the AWS-documented NIST SP 800-108 counter-mode
// KDF for deriving an ECDSA P-256 key from an existing AWS access-key pair.
// See https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html.
func deriveAWSSigV4aKey(accessKeyID, secretAccessKey string) (*ecdsa.PrivateKey, error) {
	if accessKeyID == "" || secretAccessKey == "" {
		return nil, fmt.Errorf("AWS SigV4a AKSK is required")
	}
	curve := elliptic.P256()
	nMinusTwo := new(big.Int).Sub(new(big.Int).Set(curve.Params().N), big.NewInt(2))
	limit := nMinusTwo.FillBytes(make([]byte, 32))
	inputKey := append([]byte("AWS4A"), []byte(secretAccessKey)...)
	for counter := 1; counter <= 255; counter++ {
		context := append([]byte(accessKeyID), byte(counter))
		candidate := awsSigV4aKDF(inputKey, []byte(awsSigV4aAlgorithm), context)
		if constantTimeBigEndianCompare(candidate, limit) >= 0 {
			continue
		}
		d := new(big.Int).Add(new(big.Int).SetBytes(candidate), big.NewInt(1))
		x, y := curve.ScalarBaseMult(d.Bytes())
		return &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}, nil
	}
	return nil, fmt.Errorf("AWS SigV4a key derivation exhausted its counter")
}

func awsSigV4aKDF(key, label, context []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_ = binary.Write(mac, binary.BigEndian, uint32(1))
	_, _ = mac.Write(label)
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(context)
	_ = binary.Write(mac, binary.BigEndian, uint32(256))
	return mac.Sum(nil)
}

func constantTimeBigEndianCompare(left, right []byte) int {
	if len(left) != len(right) {
		if len(left) < len(right) {
			return -1
		}
		return 1
	}
	leftLarger, rightLarger := 0, 0
	for index := range left {
		leftByte, rightByte := int(left[index]), int(right[index])
		leftWins := ((rightByte - leftByte) >> 8) & 1
		rightWins := ((leftByte - rightByte) >> 8) & 1
		leftLarger |= leftWins &^ rightLarger
		rightLarger |= rightWins &^ leftLarger
	}
	return leftLarger - rightLarger
}

func signAWSSigV4a(request *http.Request, payloadHash string, credentials AWSCredentials, service string, regionSet []string, signingTime time.Time) (string, error) {
	if request == nil || request.URL == nil {
		return "", fmt.Errorf("AWS SigV4a request is required")
	}
	if !identifierPattern.MatchString(service) || len(regionSet) == 0 {
		return "", fmt.Errorf("AWS SigV4a requires service and region_set")
	}
	privateKey, err := deriveAWSSigV4aKey(credentials.AccessKeyID, credentials.SecretAccessKey)
	if err != nil {
		return "", err
	}
	now := signingTime.UTC()
	request.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Region-Set", strings.Join(regionSet, ","))
	if credentials.SessionToken != "" {
		request.Header.Set("X-Amz-Security-Token", credentials.SessionToken)
	}
	if strings.EqualFold(service, "s3") {
		request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	}
	if request.ContentLength > 0 {
		request.Header.Set("Content-Length", fmt.Sprintf("%d", request.ContentLength))
	}
	if request.URL.Port() == "443" {
		request.Host = request.URL.Hostname()
	}
	canonicalHeaderValue, signedHeaders := canonicalHeaders(request, func(name string) bool {
		switch name {
		case "authorization", "user-agent", "x-amzn-trace-id", "transfer-encoding", "connection":
			return false
		default:
			return true
		}
	})
	path := canonicalURI(request.URL)
	if !strings.EqualFold(service, "s3") {
		path = httpbinding.EscapePath(path, false)
	}
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(request.Method), path, canonicalQuery(request.URL.Query()), canonicalHeaderValue, signedHeaders, payloadHash,
	}, "\n")
	scope := now.Format("20060102") + "/" + strings.ToLower(service) + "/aws4_request"
	stringToSign := strings.Join([]string{
		awsSigV4aAlgorithm, now.Format("20060102T150405Z"), scope, sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	digest := sha256.Sum256([]byte(stringToSign))
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("ECDSA sign: %w", err)
	}
	request.Header.Set("Authorization", awsSigV4aAlgorithm+" Credential="+credentials.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+hex.EncodeToString(signature))
	return stringToSign, nil
}

#!/usr/bin/env bash
# Shared helpers for the hand-written TC3 reference clients.

hash_extract() { awk '{print $NF}'; }
sha256_hex() { openssl sha256 -hex | hash_extract; }

# Keep raw and derived HMAC keys out of child-process arguments. The first two
# stdin lines describe the key representation; the remainder is the message.
hmac_sha256_hex() {
  local key=$1
  local message=$2
  local key_format=${3:-text}
  {
    printf '%s\n' "${key_format}"
    printf '%s\n' "${key}"
    printf '%s' "${message}"
  } | env -u TENCENTCLOUD_SECRET_ID -u TENCENTCLOUD_SECRET_KEY python3 -c '
import binascii
import hashlib
import hmac
import sys

key_format = sys.stdin.buffer.readline().rstrip(b"\n")
key = sys.stdin.buffer.readline().rstrip(b"\n")
if key_format == b"hex":
    key = binascii.unhexlify(key)
message = sys.stdin.buffer.read()
print(hmac.new(key, message, hashlib.sha256).hexdigest())
'
}

write_tc3_headers() {
  local target=$1
  local authorization=$2
  local host=$3
  local action=$4
  local timestamp=$5
  local version=$6
  local region=$7

  chmod 600 "${target}"
  printf '%s\n' \
    "Authorization: ${authorization}" \
    "Content-Type: application/json; charset=utf-8" \
    "Host: ${host}" \
    "X-TC-Action: ${action}" \
    "X-TC-Timestamp: ${timestamp}" \
    "X-TC-Version: ${version}" \
    "X-TC-Region: ${region}" \
    "X-TC-Token:" > "${target}"
}

# Tencent Cloud official references

- Tencent Cloud API reference: https://cloud.tencent.com/document/api
- Direct Connect current API introduction confirming old APIs remain functional: https://cloud.tencent.com/document/product/216/18403
- Direct Connect API 2017 authentication and exact request-path signing: https://cloud.tencent.com/document/product/216/1714
- Direct Connect API 2017 request example: https://cloud.tencent.com/document/product/216/9343
- Realtime Speech Recognition WebSocket protocol and HMAC-SHA1 signing: https://cloud.tencent.com/document/product/1093/48982
- Virtual-number human-detection WebSocket protocol and HMAC-SHA1 signing: https://cloud.tencent.com/document/product/1093/94490
- New SOE oral-evaluation WebSocket protocol and HMAC-SHA1 signing: https://cloud.tencent.com/document/product/1774/107497
- Realtime speech translation WebSocket protocol, optional binary TTS, and `final=1/2` semantics: https://cloud.tencent.com/document/api/1093/127565
- Voice conversion WebSocket HMAC-SHA1 signing and big-endian JSON/audio framing: https://cloud.tencent.com/document/product/1664/85973
- Official Tencent Cloud Speech SDK voice-conversion transport: https://github.com/TencentCloud/tencentcloud-speech-sdk-python/blob/master/vc/speech_convertor_ws.py
- Official Tencent Cloud Speech SDK for Go: https://github.com/TencentCloud/tencentcloud-speech-sdk-go
- MPS WebSocket recognition protocol, MPS-specific TC3 signing, binary audio frames, and `ProcessEof`: https://cloud.tencent.com/document/product/862/121186
- MPS smart-subtitle private-audio integration and official Python sample: https://cloud.tencent.com/document/product/862/89091
- MPS streaming TTS protocol, signature, text messages, binary audio, and official Python sample: https://cloud.tencent.com/document/product/862/133241
- Standard realtime TTS WebSocket protocol, HMAC-SHA1 signature, status/subtitle text frames, and binary audio: https://cloud.tencent.com/document/product/1073/94308
- Official Tencent Cloud Speech SDK realtime TTS WebSocket implementation: https://github.com/TencentCloud/tencentcloud-speech-sdk-go/blob/master/tts/speechwssynthesizer.go
- API 3.0 common parameters: https://intl.cloud.tencent.com/document/product/1005/34677
- TC3-HMAC-SHA256 signature: https://intl.cloud.tencent.com/document/product/627/64494
- API 3.0 Signature v1 HmacSHA1/HmacSHA256 canonical request and fixed example: https://cloud.tencent.com/document/api/583/17239
- API 3.0 request structure documenting form-urlencoded POST as v1-only and JSON/multipart as TC3: https://cloud.tencent.com/document/product/1278/46713
- COS REST request signature: https://intl.cloud.tencent.com/document/product/436/7778
- CAM resource role and temporary credentials: https://cloud.tencent.com/document/product/598/85616
- Tencent Cloud API Explorer: https://console.cloud.tencent.com/api/explorer
- COS GET Object and Range download: https://intl.cloud.tencent.com/document/product/436/7753

The universal adapter signs API 3.0 requests with recommended TC3 or the still-documented v1 HmacSHA1/HmacSHA256 query/form protocol, legacy qcloud API 2017 requests at their exact `/v2/index.php` path, COS data-plane requests with the COS REST signature, realtime ASR, virtual-number detection, SOE evaluation, speech-translation, voice-conversion, and standard realtime TTS WSS requests using their official raw canonical query plus HMAC-SHA1 algorithms, and MPS recognition/TTS WSS requests using their documented TC3 canonical `post` requests. All signed WSS URLs remain internal to the connection dialer. It never executes TCCLI.

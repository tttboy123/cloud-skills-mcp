# Baidu AI Cloud official references

- API center: https://cloud.baidu.com/doc/API/index.html
- API getting started: https://cloud.baidu.com/doc/APIGUIDE/s/1k1mysgan
- Generate authentication string: https://cloud.baidu.com/doc/Reference/s/njwvz1yfu
- IAM STS: https://cloud.baidu.com/doc/IAM/s/Qjwvyc8ov
- SDK AK/SK and STS credentials: https://cloud.baidu.com/doc/IAM/s/ll9cep8sh
- BCC read-only instance list (`GET /v2/instance`): https://cloud.baidu.com/doc/BCC/s/Xjwvyodmx

The adapter follows `bce-auth-v1/{accessKeyId}/{timestamp}/{expiration}/{signedHeaders}/{signature}`, signs `host` plus applicable standard and `x-bce-*` headers, and carries the IAM/STS session token only inside the signed request.

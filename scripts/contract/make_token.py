#!/usr/bin/env python3
"""Imprime un JWT HS256 de prueba para el contrato (solo biblioteca estándar).

Uso: make_token.py <sub-uuid> <secret> [segundos-de-vida]
"""
import base64
import hashlib
import hmac
import json
import sys
import time


def b64(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def main() -> int:
    if len(sys.argv) < 3:
        print("uso: make_token.py <sub-uuid> <secret> [segundos]", file=sys.stderr)
        return 2
    sub, secret = sys.argv[1], sys.argv[2]
    ttl = int(sys.argv[3]) if len(sys.argv) > 3 else 3600
    now = int(time.time())
    header = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
    payload = b64(json.dumps({"sub": sub, "iat": now, "exp": now + ttl}, separators=(",", ":")).encode())
    signing = f"{header}.{payload}".encode()
    sig = b64(hmac.new(secret.encode(), signing, hashlib.sha256).digest())
    print(f"{header}.{payload}.{sig}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

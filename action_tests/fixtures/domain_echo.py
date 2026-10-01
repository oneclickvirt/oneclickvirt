#!/usr/bin/env python3
"""Small dependency-free HTTP/WebSocket echo for live domain proxy checks."""

import base64
import hashlib
import http.server
import os
import struct


MARKER = b"oneclickvirt-live-domain-echo\n"
GUID = b"258EAFA5-E914-47DA-95CA-C5AB0DC85B11"


def read_exact(stream, count):
    data = bytearray()
    while len(data) < count:
        chunk = stream.read(count - len(data))
        if not chunk:
            raise EOFError("WebSocket peer closed")
        data.extend(chunk)
    return bytes(data)


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        if self.headers.get("Upgrade", "").lower() == "websocket":
            self.websocket()
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(MARKER)))
        self.end_headers()
        self.wfile.write(MARKER)

    def websocket(self):
        key = self.headers.get("Sec-WebSocket-Key", "")
        if not key:
            self.send_error(400, "Missing Sec-WebSocket-Key")
            return
        accept = base64.b64encode(hashlib.sha1(key.encode() + GUID).digest()).decode()
        self.send_response(101, "Switching Protocols")
        self.send_header("Upgrade", "websocket")
        self.send_header("Connection", "Upgrade")
        self.send_header("Sec-WebSocket-Accept", accept)
        self.end_headers()
        self.connection.settimeout(15)
        for _ in range(4):
            first, second = read_exact(self.rfile, 2)
            length = second & 0x7F
            if length == 126:
                length = struct.unpack("!H", read_exact(self.rfile, 2))[0]
            elif length == 127:
                length = struct.unpack("!Q", read_exact(self.rfile, 8))[0]
            if length > 4096 or not second & 0x80:
                break
            mask = read_exact(self.rfile, 4)
            payload = read_exact(self.rfile, length)
            payload = bytes(byte ^ mask[index % 4] for index, byte in enumerate(payload))
            opcode = first & 0x0F
            if opcode == 0x8:
                break
            if opcode not in (0x1, 0x9):
                break
            response_opcode = 0xA if opcode == 0x9 else 0x1
            self.wfile.write(bytes([0x80 | response_opcode, len(payload)]) + payload)
            self.wfile.flush()
        self.close_connection = True


if __name__ == "__main__":
    port = int(os.environ.get("OCV_DOMAIN_ECHO_PORT", "18080"))
    http.server.ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()

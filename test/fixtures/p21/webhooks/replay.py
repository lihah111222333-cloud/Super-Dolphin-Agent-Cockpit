"""mitmproxy script: P21 webhook scenarios.

Run: mitmdump -s test/fixtures/p21/webhooks/replay.py -p 18443
"""
from mitmproxy import http


def request(flow: http.HTTPFlow) -> None:
    path = flow.request.path
    if path == "/p21/redirect-private":
        flow.response = http.Response.make(302, b"", {"Location": "http://10.0.0.1/"})
    elif path == "/p21/redirect-loopback":
        flow.response = http.Response.make(302, b"", {"Location": "http://127.0.0.1/"})
    elif path == "/p21/echo":
        flow.response = http.Response.make(200, flow.request.raw_content,
                                           {"Content-Type": "application/json"})
    elif path == "/p21/flaky":
        flow.response = http.Response.make(503, b"flaky")

"""Serve a folder on localhost with byte ranges, for the light show page
(eval.lightshow): the page seeks in the set's audio, which needs ranges, and
http.server's own server looks the host up in DNS when it starts, which can
hang without a network.

  python -m eval.serve data/mine/lights        # then open http://127.0.0.1:8766/<name>.html
"""

import argparse
import functools
import os
import re
import socketserver
from http.server import SimpleHTTPRequestHandler

RANGE = re.compile(r"bytes=(\d*)-(\d*)$")


class Handler(SimpleHTTPRequestHandler):
    """Static files, answering a single byte range with 206."""

    def send_head(self):
        m = RANGE.match(self.headers.get("Range") or "")
        path = self.translate_path(self.path)
        if not m or not os.path.isfile(path):
            return super().send_head()
        size = os.path.getsize(path)
        if m.group(1):
            start = int(m.group(1))
            end = min(int(m.group(2)) if m.group(2) else size - 1, size - 1)
        else:
            start, end = max(0, size - int(m.group(2) or 0)), size - 1
        f = open(path, "rb")  # the base class closes it after copyfile
        f.seek(start)
        self.send_response(206)
        self.send_header("Content-Type", self.guess_type(path))
        self.send_header("Content-Range", f"bytes {start}-{end}/{size}")
        self.send_header("Content-Length", str(end - start + 1))
        self.end_headers()
        self._left = end - start + 1
        return f

    def copyfile(self, source, outputfile) -> None:
        left = getattr(self, "_left", None)
        if left is None:
            super().copyfile(source, outputfile)
            return
        while left > 0:
            buf = source.read(min(65536, left))
            if not buf:
                break
            outputfile.write(buf)
            left -= len(buf)

    def end_headers(self) -> None:
        self.send_header("Accept-Ranges", "bytes")
        super().end_headers()


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("folder")
    ap.add_argument("--port", type=int, default=8766)
    args = ap.parse_args()
    with Server(("127.0.0.1", args.port), functools.partial(Handler, directory=args.folder)) as s:
        print(f"serving {args.folder} on http://127.0.0.1:{args.port}/", flush=True)
        s.serve_forever()


if __name__ == "__main__":
    main()

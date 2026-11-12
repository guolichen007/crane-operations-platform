import http.server, json
S={"userId":"perf","displayName":"perf","projects":[{"projectId":"P001","projectName":"Project-A","role":"viewer"}],"csrfToken":"x"*32,"expiresAt":"2099-01-01T00:00:00Z","idleExpiresAt":"2099-01-01T00:00:00Z"}
class H(http.server.BaseHTTPRequestHandler):
  def do_GET(self):
    b=json.dumps(S if self.path.startswith('/auth/session') else []).encode()
    self.send_response(200); self.send_header('Content-Type','application/json'); self.send_header('Cache-Control','no-store'); self.send_header('Content-Length',str(len(b))); self.end_headers(); self.wfile.write(b)
  def log_message(self,*a): pass
http.server.ThreadingHTTPServer(('127.0.0.1',18081),H).serve_forever()

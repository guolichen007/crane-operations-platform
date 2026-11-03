"""Nginx syntax with ephemeral synthetic TLS, no sockets and no network."""
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[3]
class IngressSyntax(unittest.TestCase):
 def test_shared_ingress_with_real_nginx_parser(self):
  with tempfile.TemporaryDirectory(prefix='baobei-ingress-') as tmp:
   root=Path(tmp)
   subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=synthetic.example.test','-keyout',str(root/'privkey.pem'),'-out',str(root/'fullchain.pem')],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
   common=(ROOT/'deploy/baobei/ingress/baobei-http-common.conf.example').read_text()
   server=(ROOT/'deploy/baobei/ingress/baobei.youdaozd.com.conf.example').read_text().replace('/etc/nginx/tls/baobei',str(root)).replace('/var/www/acme',str(root/'web'))
   config=root/'nginx.conf'
   config.write_text('pid '+str(root/'nginx.pid')+';\nerror_log stderr crit;\nevents {}\nhttp {\n'+''.join(k+' '+str(root/k)+';\n' for k in ['client_body_temp_path','proxy_temp_path','fastcgi_temp_path','uwsgi_temp_path','scgi_temp_path'])+common+'\n'+server+'\n}\n')
   result=subprocess.run(['nginx','-t','-c',str(config),'-e','/dev/stderr'],capture_output=True,text=True)
   self.assertEqual(result.returncode,0,result.stderr)

if __name__=='__main__':unittest.main()

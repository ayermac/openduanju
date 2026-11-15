import json, os, secrets, signal, subprocess, time, urllib.request
from pathlib import Path
root=Path(__file__).resolve().parent.parent
cache=root/'.cache/runtime-smoke';cache.mkdir(parents=True,exist_ok=True)
config_dir=cache/'config';config_dir.mkdir(exist_ok=True)
password=secrets.token_hex(16)
name='openduanju-bootstrap-smoke-'+secrets.token_hex(4)
envfile=cache/'mysql.env'
envfile.write_text('MYSQL_ROOT_PASSWORD='+password+'\nMYSQL_DATABASE=openduanju_bootstrap\n');envfile.chmod(0o600)
container=None;server=None
summary={'scope':'official scaffold only, isolated MySQL, no business integration','result':'failed'}
log=None
try:
 r=subprocess.run(['docker','run','--pull','never','--rm','-d','--name',name,'--label','openduanju.scope=bootstrap-smoke','--env-file',str(envfile),'-p','127.0.0.1:13381:3306','mysql:8.4'],capture_output=True,text=True,timeout=30)
 if r.returncode:raise RuntimeError('isolated mysql container start failed: '+r.stderr[-300:])
 container=r.stdout.strip();print('Isolated MySQL started',flush=True)
 ready=False
 for _ in range(45):
  r=subprocess.run(['docker','exec','--env-file',str(envfile),container,'sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -h127.0.0.1 -uroot -Dopenduanju_bootstrap -Nse "SELECT 1"'],capture_output=True,text=True,timeout=5)
  if r.returncode==0 and r.stdout.strip()=='1':ready=True;break
  time.sleep(2)
 if not ready:raise RuntimeError('isolated MySQL readiness timed out')
 conf=(root/'server/configs/config.yaml').read_text().replace('0.0.0.0:8000','127.0.0.1:28000').replace('0.0.0.0:9000','127.0.0.1:29000')
 (config_dir/'config.yaml').write_text(conf)
 env=dict(os.environ);env['KRATOS_DATABASE_SOURCE']='root:'+password+'@tcp(127.0.0.1:13381)/openduanju_bootstrap?parseTime=true&loc=UTC&charset=utf8mb4'
 log=(cache/'server.log').open('w')
 server=subprocess.Popen([str(root/'server/bin/openduanju-server'),'-conf',str(config_dir)],cwd=str(root/'server'),env=env,stdout=log,stderr=subprocess.STDOUT)
 base='http://127.0.0.1:28000'
 ready=False
 for _ in range(40):
  if server.poll() is not None:raise RuntimeError('server exited before HTTP readiness')
  try:
   with urllib.request.urlopen(base+'/v1/todos/list',timeout=1) as response:
    if response.status==200:ready=True;break
  except Exception:time.sleep(.5)
 if not ready:raise RuntimeError('server HTTP readiness timed out')
 req=urllib.request.Request(base+'/v1/todos/create',data=json.dumps({'title':'foundation-runtime-smoke','content':'isolated official template validation'}).encode(),headers={'Content-Type':'application/json'},method='POST')
 with urllib.request.urlopen(req,timeout=5) as response:created=json.load(response)
 assert created.get('id'), 'create returned no id'
 with urllib.request.urlopen(base+'/v1/todos/'+str(created['id']),timeout=5) as response:read=json.load(response)
 assert read.get('title')=='foundation-runtime-smoke','persisted title mismatch'
 summary.update(result='passed',checks=['mysql isolated start','official server start','HTTP list readiness','create persisted Todo','read same Todo'],http_port=28000,grpc_port=29000)
 print('Official API + real isolated MySQL CRUD: PASS',flush=True)
except Exception as exc:
 summary['error']=str(exc).replace(password,'[REDACTED]')
 print('Runtime smoke failed: '+summary['error'],flush=True)
finally:
 if server is not None and server.poll() is None:
  server.send_signal(signal.SIGTERM)
  try:server.wait(timeout=10)
  except subprocess.TimeoutExpired:server.kill();server.wait(timeout=5)
 if log:log.close()
 if container:
  cleanup=subprocess.run(['docker','rm','-f','-v',container],capture_output=True,text=True,timeout=20)
  summary['container_cleanup']=cleanup.returncode==0 or 'No such container' in cleanup.stderr
 if envfile.exists():envfile.unlink()
 logfile=cache/'server.log'
 if logfile.exists():logfile.write_text(logfile.read_text().replace(password,'[REDACTED]'))
 (cache/'result.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
 print(json.dumps(summary,ensure_ascii=False),flush=True)
raise SystemExit(0 if summary['result']=='passed' and summary.get('container_cleanup') else 1)

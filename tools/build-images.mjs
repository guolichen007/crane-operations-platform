// Windows packages binaries built with the pinned local toolchain. CI builds
// the same sources with Dockerfile.backend and Dockerfile.web.
import { spawnSync } from 'node:child_process';
import { mkdirSync, openSync, closeSync, cpSync, existsSync } from 'node:fs';
import { resolve, dirname, join } from 'node:path';
const docker='C:/Program Files/Docker/Docker/resources/bin/docker.exe';
const go=existsSync('tools/.cache/go/bin/go.exe')?resolve('tools/.cache/go/bin/go.exe'):'go';
const context=resolve('tools/.cache/packaging');mkdirSync(context,{recursive:true});
function run(cmd,args,options={}){const r=spawnSync(cmd,args,{stdio:'inherit',...options});if(r.error)throw r.error;if(r.status!==0)throw Error(`${cmd} failed (${r.status})`);}
const names=[['cloud-api','services/cloud-api/cmd/cloud-api'],['remote-gateway','services/remote-gateway'],['device-simulator','simulators/device-simulator']];
for(const [name,target]of names)run(go,['build','-trimpath','-ldflags=-s -w','-o',join(context,name),'./'+target],{env:{...process.env,GOOS:'linux',GOARCH:'amd64',CGO_ENABLED:'0',GOCACHE:resolve('tools/.cache/gobuild'),GOMODCACHE:resolve('tools/.cache/gomod'),GOPATH:resolve('tools/.cache/gopath')}});
run(process.execPath,[join(dirname(process.execPath),'node_modules/npm/bin/npm-cli.js'),'run','build'],{env:{...process.env,VITE_DATA_MODE:'api'}});
cpSync('apps/web-console/dist',join(context,'web'),{recursive:true});
for(const file of ['Dockerfile.packaging','Dockerfile.web-packaging'])cpSync('deploy/'+file,join(context,file));
cpSync('deploy/nginx/default.conf',join(context,'default.conf'));
const tar=resolve('tools/.cache/packaging.tar');run('tar',['-cf',tar,'-C',context,'.']);
for(const [name]of [...names,['web-console']]){
 const fd=openSync(tar,'r');try{run(docker,['build','-f',name==='web-console'?'Dockerfile.web-packaging':'Dockerfile.packaging','-t',`crane-phase0-${name}`,'--build-arg',`EXECUTABLE=${name}`,'-'],{stdio:[fd,'inherit','inherit']});}finally{closeSync(fd);}
}

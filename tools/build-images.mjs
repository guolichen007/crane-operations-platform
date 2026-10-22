// Stream a tar context: Docker Desktop BuildKit on Windows rejects non-ASCII
// session metadata when a Chinese workspace is exposed as a local context.
import { spawnSync } from 'node:child_process';
import { mkdirSync, openSync, closeSync } from 'node:fs';
import { resolve } from 'node:path';
const docker=process.platform==='win32'?'C:/Program Files/Docker/Docker/resources/bin/docker.exe':'docker';
mkdirSync('tools/.cache',{recursive:true});const tar=resolve('tools/.cache/docker-context.tar');
const excludes=['.git','local-private','node_modules','tools/.cache','release','deploy/compose/runtime','apps/web-console/dist','apps/web-console/dist-offline','playwright-report','test-results*'];
let result=spawnSync('tar',['-cf',tar,...excludes.map(p=>'--exclude='+p),'.'],{stdio:'inherit'});if(result.status!==0)throw Error('Build context archive failed');
for(const [name,target]of [['cloud-api','services/cloud-api/cmd/cloud-api'],['remote-gateway','services/remote-gateway'],['device-simulator','simulators/device-simulator'],['web-console',null]]){
 const fd=openSync(tar,'r');try{result=spawnSync(docker,['build','-f',target?'deploy/Dockerfile.backend':'deploy/Dockerfile.web','-t',`crane-phase0-${name}`,...(target?['--build-arg',`TARGET=${target}`]:[]),'-'],{stdio:[fd,'inherit','inherit'],env:process.env});if(result.status!==0)throw Error(`${name} build failed`);}finally{closeSync(fd);}
}

import {spawnSync} from 'node:child_process';
const docker=(...args)=>{
 const r=spawnSync('docker',[...(process.env.BAOBEI_DOCKER_CONTEXT?['--context',process.env.BAOBEI_DOCKER_CONTEXT]:[]),...args],{stdio:'inherit'});
 if(r.error)throw r.error;if(r.status)throw Error('Isolated Baobei cert test failed');
};
docker('build','-t','baobei-cert-test:local','tests/baobei/cert');
docker('run','--rm','--network','none','--read-only','--tmpfs','/tmp:rw,nosuid,nodev,exec,size=128m',
 '-v',process.cwd()+'/deploy/baobei:/src/deploy/baobei:ro',
 '-v',process.cwd()+'/tests/baobei/cert:/src/tests/baobei/cert:ro',
 'baobei-cert-test:local','-m','unittest','discover','-s','tests/baobei/cert','-v');

import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location('baobei_publish', ROOT/'deploy/baobei/publish/publish.py')
publish = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publish)
SHA = '1'*40
DIGEST = 'sha256:'+'a'*64

class PublishTests(unittest.TestCase):
    def ci(self, url):
        if '/jobs?' in url:
            return {'total_count':8,'jobs':[{'name':n,'status':'completed','conclusion':'success'} for n in sorted(publish.GATES)]}
        return {'workflow_runs':[{'id':7,'run_attempt':2,'head_sha':SHA,'head_branch':publish.BRANCH,'path':'.github/workflows/baobei-ci.yml','event':'push','status':'completed','conclusion':'success'}]}

    def test_ci_same_sha_branch_workflow_latest_attempt_and_eight_gates(self):
        self.assertEqual(publish.verify_ci(SHA,self.ci),7)
        for key,bad in [('head_sha','2'*40),('head_branch','main'),('path','.github/workflows/other.yml'),('status','in_progress'),('conclusion','failure')]:
            def mutate(url):
                data=self.ci(url)
                if 'workflow_runs' in data:data['workflow_runs'][0][key]=bad
                return data
            with self.subTest(key=key),self.assertRaises(publish.ClosureError):publish.verify_ci(SHA,mutate)
        for mode in ['missing','skipped','duplicate','latest-failed','attempt-url']:
            def mutate(url):
                data=self.ci(url)
                if 'jobs' in data:
                    self.assertIn('/attempts/2/jobs?',url)
                    if mode=='missing':data['jobs'].pop()
                    if mode=='skipped':data['jobs'][0]['conclusion']='skipped'
                    if mode=='duplicate':data['jobs'][0]['name']=data['jobs'][1]['name']
                if mode=='latest-failed' and 'workflow_runs' in data:data['workflow_runs'].append({**data['workflow_runs'][0],'id':8,'conclusion':'failure'})
                return data
            if mode=='attempt-url':self.assertEqual(publish.verify_ci(SHA,mutate),7)
            else:
                with self.subTest(mode=mode),self.assertRaises(publish.ClosureError):publish.verify_ci(SHA,mutate)
        with self.assertRaises(publish.ClosureError):publish.verify_ci('main',self.ci)

    def image(self,ref):
        return [{'Id':'sha256:'+'b'*64,'RepoDigests':[ref],'Config':{'Labels':{'org.opencontainers.image.revision':SHA,'org.opencontainers.image.source':publish.SOURCE}}}]

    def test_manifest_digest_and_pulled_labels_not_image_id(self):
        ref=publish.IMAGES['web']+'@'+DIGEST
        publish.verify_image(self.image(ref),ref,SHA)
        self.assertEqual(publish.registry_digest('Name: example\nDigest: '+DIGEST+'\n'),DIGEST)
        for value in ['sha256:'+'b'*64,'Digest: sha256:short','Digest: '+DIGEST+'\nDigest: '+DIGEST]:
            with self.assertRaises(publish.ClosureError):publish.registry_digest(value)
        for mode in ['revision','source','digest','local-id']:
            image=self.image(ref)
            if mode=='revision':image[0]['Config']['Labels']['org.opencontainers.image.revision']='2'*40
            if mode=='source':image[0]['Config']['Labels']['org.opencontainers.image.source']='https://example.test'
            if mode=='digest':image[0]['RepoDigests']=[]
            if mode=='local-id':ref='baobei-web@'+DIGEST
            with self.subTest(mode=mode),self.assertRaises(publish.ClosureError):publish.verify_image(image,ref,SHA)

    def fake(self,args,env=None,stdin=None):
        self.calls.append((args,env,stdin))
        if args[:3]==['git','rev-parse','HEAD']:return SHA+'\n'
        if args[:2]==['git','status']:return ''
        if args[:2]==['git','merge-base']:return ''
        if args[:2]==['docker','login']:
            self.assertEqual(stdin,'synthetic-workflow-token\n')
            self.auth=env['DOCKER_CONFIG']
        if args[:2]==['docker','build']:
            self.assertNotIn('GITHUB_TOKEN',env)
            self.assertNotIn('synthetic-workflow-token',' '.join(args))
            self.assertIn('SOURCE_SHA='+SHA,args)
        if args[:4]==['docker','buildx','imagetools','inspect']:
            if self.failure=='manifest':raise publish.ClosureError('fake manifest failure')
            return 'Name: '+args[-1]+'\nDigest: '+DIGEST+'\n'
        if args[:2]==['docker','pull'] and self.failure=='private' and env['DOCKER_CONFIG']!=self.auth:raise publish.ClosureError('private package')
        if args[:3]==['docker','image','inspect']:
            image=self.image(args[-1])
            if self.failure=='revision':image[0]['Config']['Labels']['org.opencontainers.image.revision']='2'*40
            return json.dumps(image)
        if args[:2]==['docker','push'] and (self.failure=='push' or (self.failure=='api-push' and 'baobei-api:' in args[-1])):raise publish.ClosureError('fake push failure')
        return ''

    def test_pipeline_success_and_all_failure_paths_fail_closed(self):
        for failure in ['', 'manifest','private','revision','push','api-push']:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp)
                for name in ['contracts/project-intake/openapi.yaml','contracts/project-intake/ProjectIntakeTransferV1.schema.json','services/project-intake-api/migrations/00002_auth.sql']:
                    p=root/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text('synthetic')
                self.calls=[];self.failure=failure;self.auth=None
                out=root/'local-private/baobei/registry-release'/SHA
                out.mkdir(parents=True);(out/'deployment-manifest.json').write_text('STALE')
                if failure:
                    with self.assertRaises(publish.ClosureError):publish.publish(root,SHA,'123-1','synthetic-workflow-token','synthetic-actor',self.fake,self.ci)
                    self.assertFalse((out/'deployment-manifest.json').exists())
                else:
                    result=publish.publish(root,SHA,'123-1','synthetic-workflow-token','synthetic-actor',self.fake,self.ci)
                    self.assertTrue(result['registry_published']);self.assertTrue(result['public_pull_verified'])
                    self.assertEqual(result['schema_version'],'00002')
                    self.assertEqual(result['web_registry_digest'],DIGEST)
                    self.assertEqual(result['web_registry_ref'],publish.IMAGES['web']+'@'+DIGEST)
                    self.assertEqual(result,json.loads((out/'deployment-manifest.json').read_text()))
                    self.assertEqual(len([a for a,_,_ in self.calls if a[:2]==['docker','pull']]),4)
                status=json.loads((out/'publish-status.json').read_text())
                self.assertEqual(status['READY_FOR_PRODUCTION'],'NO')
                self.assertEqual(status['S1B_PUBLIC_DEPLOY'],'NOT_AUTHORIZED')
                self.assertTrue(any(a[:2]==['docker','logout'] for a,_,_ in self.calls))
                for _,env,_ in self.calls:
                    if env and 'DOCKER_CONFIG' in env:self.assertFalse(Path(env['DOCKER_CONFIG']).exists())

if __name__ == '__main__':unittest.main()

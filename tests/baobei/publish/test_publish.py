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
REF = publish.RELEASE_PREFIX + SHA
ENV = {
    'GITHUB_REF': REF, 'GITHUB_SHA': SHA, 'GITHUB_REPOSITORY': publish.REPOSITORY,
    'GITHUB_EVENT_NAME': 'push', 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '1',
    'GITHUB_ACTOR_ID': '100',
}
EVENT = {'ref': REF, 'after': SHA, 'created': True, 'forced': False, 'deleted': False}

class PublishTests(unittest.TestCase):
    def ci(self, url):
        if '/jobs?' in url:
            return {'total_count':8,'jobs':[{'name':n,'status':'completed','conclusion':'success'} for n in sorted(publish.GATES)]}
        return {'workflow_runs':[{'id':7,'run_attempt':2,'head_sha':SHA,'head_branch':publish.BRANCH,'path':'.github/workflows/baobei-ci.yml','event':'push','status':'completed','conclusion':'success'}]}

    def release_api(self, url):
        if url.endswith('/approvals'):
            return [{'state': 'approved', 'user': {'id': 200},
                     'environments': [{'id': 31, 'name': publish.ENVIRONMENT}]}]
        if '/deployment-branch-policies?' in url:
            return {'total_count': 1, 'branch_policies': [
                {'name': REF.removeprefix('refs/heads/'), 'type': 'branch'}]}
        if url.endswith('/' + publish.ENVIRONMENT):
            return {'id': 31, 'name': publish.ENVIRONMENT, 'protection_rules': [
                {'type': 'required_reviewers', 'reviewers': [{'type': 'User', 'reviewer': {'id': 200}}],
                 'prevent_self_review': True}],
                'deployment_branch_policy': {'protected_branches': False, 'custom_branch_policies': True}}
        return self.ci(url)

    def test_release_ref_requires_new_official_push_and_exact_source(self):
        self.assertEqual(publish.release_branch(SHA, ENV, EVENT), REF.removeprefix('refs/heads/'))
        for key, value in [
            ('GITHUB_REF', 'refs/heads/product/baobei-v1'),
            ('GITHUB_REF', publish.RELEASE_PREFIX + SHA[:-1]),
            ('GITHUB_REF', publish.RELEASE_PREFIX + '2'*40),
            ('GITHUB_SHA', '2'*40), ('GITHUB_REPOSITORY', 'fork/crane-operations-platform'),
            ('GITHUB_EVENT_NAME', 'workflow_dispatch'), ('GITHUB_RUN_ATTEMPT', '2'),
        ]:
            with self.subTest(key=key, value=value), self.assertRaises(publish.ClosureError):
                publish.release_branch(SHA, {**ENV, key: value}, EVENT)
        for key, value in [('created', False), ('forced', True), ('deleted', True), ('after', '2'*40)]:
            with self.subTest(key=key), self.assertRaises(publish.ClosureError):
                publish.release_branch(SHA, ENV, {**EVENT, key: value})
        def wrong_head(args):
            self.assertEqual(args, ['git', 'rev-parse', 'HEAD'])
            return '2'*40
        with self.assertRaisesRegex(publish.ClosureError, 'Checkout'):
            publish.verify_release(SHA, ENV, EVENT, wrong_head, self.release_api, require_approval=False)

    def test_environment_failures_stop_before_any_docker_command(self):
        for failure in ('missing', 'api-denied', 'no-reviewers', 'branch', 'tag-rule',
                        'unapproved', 'bypass', 'wrong-environment', 'unlisted-user', 'self-review'):
            def fetch(url):
                data = self.release_api(url)
                if url.endswith('/' + publish.ENVIRONMENT):
                    if failure in ('missing', 'api-denied'):
                        raise PermissionError(failure)
                    if failure == 'no-reviewers':
                        data['protection_rules'] = []
                    if failure == 'self-review':
                        data['protection_rules'][0]['reviewers'][0]['reviewer']['id'] = 100
                if '/deployment-branch-policies?' in url:
                    if failure == 'branch':
                        data['branch_policies'][0]['name'] = 'release/*'
                    if failure == 'tag-rule':
                        data['branch_policies'][0]['type'] = 'tag'
                if url.endswith('/approvals'):
                    if failure == 'unapproved':
                        return []
                    if failure == 'bypass':
                        data[0]['state'] = 'bypassed'
                    if failure == 'wrong-environment':
                        data[0]['environments'][0]['id'] = 30
                    if failure == 'unlisted-user':
                        data[0]['user']['id'] = 999
                    if failure == 'self-review':
                        data[0]['user']['id'] = 100
                return data

            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as tmp:
                self.calls = []
                with self.assertRaisesRegex(publish.ClosureError, 'RELEASE_ENV_NOT_VERIFIED'):
                    publish.publish(tmp, SHA, ENV, EVENT, 'synthetic-workflow-token',
                                    'synthetic-actor', self.fake, fetch)
                self.assertTrue(all(args[0] == 'git' for args, _, _ in self.calls))
                self.assertFalse(list(Path(tmp).rglob('deployment-manifest.json')))

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
                    with self.assertRaises(publish.ClosureError):publish.publish(root,SHA,ENV,EVENT,'synthetic-workflow-token','synthetic-actor',self.fake,self.release_api)
                    self.assertFalse((out/'deployment-manifest.json').exists())
                else:
                    result=publish.publish(root,SHA,ENV,EVENT,'synthetic-workflow-token','synthetic-actor',self.fake,self.release_api)
                    self.assertTrue(result['registry_published']);self.assertTrue(result['public_pull_verified'])
                    self.assertEqual(result['schema_version'],'00002')
                    self.assertEqual(result['web_registry_digest'],DIGEST)
                    self.assertEqual(result['web_registry_ref'],publish.IMAGES['web']+'@'+DIGEST)
                    self.assertEqual(result,json.loads((out/'deployment-manifest.json').read_text()))
                    self.assertEqual(len([a for a,_,_ in self.calls if a[:2]==['docker','pull']]),4)
                if failure == 'private':
                    self.assertEqual(sum(args[:2] == ['docker', 'push'] for args, _, _ in self.calls), 2)
                status=json.loads((out/'publish-status.json').read_text())
                self.assertEqual(status['READY_FOR_PRODUCTION'],'NO')
                self.assertEqual(status['S1B_PUBLIC_DEPLOY'],'NOT_AUTHORIZED')
                self.assertTrue(any(a[:2]==['docker','logout'] for a,_,_ in self.calls))
                for _,env,_ in self.calls:
                    if env and 'DOCKER_CONFIG' in env:self.assertFalse(Path(env['DOCKER_CONFIG']).exists())

if __name__ == '__main__':unittest.main()

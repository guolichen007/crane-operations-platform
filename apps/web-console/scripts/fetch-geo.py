import json, pathlib, urllib.request, concurrent.futures, time
ROOT=pathlib.Path(__file__).resolve().parent.parent
GEO=ROOT/'public'/'geo'
for d in ['china','provinces','prefectures','counties']:(GEO/d).mkdir(parents=True,exist_ok=True)
def fetch(code,folder):
    out=GEO/folder/f'{code}.json'
    if out.exists():return json.loads(out.read_text(encoding='utf-8'))
    for attempt in range(3):
      try:
        req=urllib.request.Request(f'https://geo.datav.aliyun.com/areas_v3/bound/{code}_full.json',headers={'User-Agent':'Mozilla/5.0'})
        data=json.load(urllib.request.urlopen(req,timeout=18))
        # Retain every island/ring; round coordinates only. No arbitrary vertex removal.
        def rounded(v):
          if isinstance(v,list):return [rounded(x) for x in v]
          if isinstance(v,float):return round(v,5)
          return v
        for f in data['features']:f['geometry']['coordinates']=rounded(f['geometry']['coordinates'])
        out.write_text(json.dumps(data,ensure_ascii=False,separators=(',',':')),encoding='utf-8')
        return data
      except Exception:
        if attempt==2:return None
    return None
country=fetch(100000,'china')
if not country:raise RuntimeError('Country GeoJSON download failed')
manifest={}
def add_features(data,parent,file):
 for f in data['features']:
  p=f['properties'];code=p.get('adcode')
  if not isinstance(code,int) or code==100000:continue
  manifest[str(code)]={'adcode':code,'name':p['name'],'level':{'city':'prefecture','district':'county'}.get(p.get('level'),p.get('level','province')),'parent':parent,'center':p.get('centroid',p.get('center')),'boundaryFile':file,'childrenFile':None}
add_features(country,100000,'china/100000.json')
province_codes=list(manifest)
def province_job(code):return code,fetch(code,'provinces')
with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
 for code,data in pool.map(province_job,province_codes):
  if not data:continue
  manifest[code]['childrenFile']=f'provinces/{code}.json'
  add_features(data,int(code),f'provinces/{code}.json')
  print('province',code,len(data['features']),flush=True)
pref_codes=[k for k,v in manifest.items() if v['level']=='prefecture']
def pref_job(code):return code,fetch(code,'prefectures')
with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:
 for code,data in pool.map(pref_job,pref_codes):
  if not data:continue
  manifest[code]['childrenFile']=f'prefectures/{code}.json'
  add_features(data,int(code),f'prefectures/{code}.json')
manifest['100000']={'adcode':100000,'name':'全国','level':'country','parent':None,'childrenFile':'china/100000.json','boundaryFile':None,'center':[104,35]}
(GEO/'geo-manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,separators=(',',':')),encoding='utf-8')
(GEO/'source.json').write_text(json.dumps({'source':'DataV.GeoAtlas','url':'https://geo.datav.aliyun.com/areas_v3/bound/100000_full.json','retrieved':'2026-10-19','use':'内部开发原型，未经审图；正式公开展示前须替换为合规地图并标注来源/审图号','files':len(list(GEO.rglob('*.json'))),'regions':len(manifest)},ensure_ascii=False,indent=2),encoding='utf-8')
print('DONE',len(manifest),'regions',len(list(GEO.rglob('*.json'))),'files',flush=True)

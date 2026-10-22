package contract
import "testing"
func TestTopicBoundary(t *testing.T){
 for _,id:=range []string{"../x","a/+","a#","","中文"}{if _,err:=Topic("demo","project",id,"alarm");err==nil{t.Fatal("accepted invalid id",id)}}
 for suffix:=range Topics {v,err:=Topic("demo","Project-A","SIM-001",suffix);if err!=nil{t.Fatal(err)};_,_,d,s,e:=ParseTopic(v);if e!=nil||d!="SIM-001"||s!=suffix{t.Fatal(v)}}
}

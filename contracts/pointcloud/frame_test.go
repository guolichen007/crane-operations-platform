package pointcloud
import("encoding/binary";"testing")
func TestFrameAndLatest(t *testing.T){b:=make([]byte,44);copy(b,"PCD1");binary.LittleEndian.PutUint16(b[4:],1);binary.LittleEndian.PutUint32(b[20:],1);binary.LittleEndian.PutUint16(b[24:],16);if err:=Validate(b);err!=nil{t.Fatal(err)};q:=Latest{};q.Put(b);b[28]=1;q.Put(b);if q.Take()[28]!=1||q.Dropped!=1||q.Take()!=nil{t.Fatal("latest frame invariant")};if Validate(b[:30])==nil{t.Fatal("short frame accepted")}}

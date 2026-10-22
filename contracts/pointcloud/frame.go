// Package pointcloud specifies the Phase 2 binary boundary. It does not send frames.
package pointcloud
import("encoding/binary";"errors";"sync")
const HeaderBytes=28
const Stride=16
var Profiles=map[string][2]int{"LOW":{5000,1},"STANDARD":{10000,2},"ENHANCED":{20000,2},"HIGH":{30000,2}}
func Validate(frame []byte) error {
 if len(frame)<HeaderBytes||string(frame[:4])!="PCD1"||binary.LittleEndian.Uint16(frame[4:6])!=1||binary.LittleEndian.Uint16(frame[24:26])!=Stride{return errors.New("invalid frame header")}
 count:=binary.LittleEndian.Uint32(frame[20:24]);if count>30000||len(frame)!=HeaderBytes+int(count)*Stride{return errors.New("invalid frame length")};return nil
}
type Latest struct { mu sync.Mutex; frame []byte; Dropped uint64 }
func (q *Latest) Put(b []byte){q.mu.Lock();defer q.mu.Unlock();if q.frame!=nil{q.Dropped++};q.frame=append([]byte(nil),b...)}
func (q *Latest) Take()[]byte{q.mu.Lock();defer q.mu.Unlock();b:=q.frame;q.frame=nil;return b}

package contract

import (
 "errors"
 "regexp"
 "strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
func ValidID(s string) bool { return identifier.MatchString(s) }
func Topic(tenant, project, device, suffix string) (string, error) {
 if !ValidID(tenant) || !ValidID(project) || !ValidID(device) { return "", errors.New("invalid topic identity") }
 if _, ok := Topics[suffix]; !ok { return "", errors.New("unknown topic suffix") }
 return "crane/v1/"+tenant+"/"+project+"/"+device+"/"+suffix,nil
}
func ParseTopic(topic string) (tenant, project, device, suffix string, err error) {
 p:=strings.Split(topic,"/"); if len(p)<6 || p[0]!="crane" || p[1]!="v1" {err=errors.New("invalid topic");return}
 tenant,project,device,suffix=p[2],p[3],p[4],strings.Join(p[5:],"/"); _,err=Topic(tenant,project,device,suffix);return
}
func Validate(m Message) error {
 if m.SchemaVersion!=1 || !ValidID(m.DeviceId)||!ValidID(m.ProjectId)||!ValidID(m.TenantId)||!ValidID(m.MessageId)||!ValidID(m.BootId)||m.Sequence<0||m.Timestamp.IsZero(){ return errors.New("invalid envelope") }
 switch m.Kind {case "BIRTH","LWT","HEARTBEAT","SNAPSHOT","LIVE","ALARM","EVENT","WATCH_START","WATCH_RENEW","WATCH_STOP","WATCH_ACK":default:return errors.New("invalid kind")};return nil
}

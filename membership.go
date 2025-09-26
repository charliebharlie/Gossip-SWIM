package main

import (
	"fmt"
	"time"
)

type NodeID struct {
	IP   string
	Port int
}

type NodeState string

const (
	Alive   NodeState = "Alive"
	Suspect NodeState = "Suspect"
	Dead    NodeState = "Dead"
)

func (n NodeID) String() string {
	return fmt.Sprintf("%s:%d", n.IP, n.Port)
}

type Member struct {
	ID          NodeID
	Version     int
	State       NodeState
	Heartbeat   int
	LastUpdate  time.Time // used locally for T_fail and T_suspicion
	Disseminate int       // counter for piggybacking, if a member has a positive value means we should send this member as an update
}

type Ping_State string

const (
	FirstPing  Ping_State = "First Ping"
	SecondPing Ping_State = "Second Ping"
)

type Pending struct {
	ID        NodeID
	PingState Ping_State
	SentTime  time.Time
	Stop      chan struct{}
}

func (m Member) String() string {
	return fmt.Sprintf("[%v, State=%s, Heartbeat=%d Disseminate=%d]",
		m.ID, m.State, m.Heartbeat, m.Disseminate)
}

type Message struct {
	Type             string // "gossip", "join", "join_ack", "leave"
	Sender           Member
	MembershipUpdate []Member // changes (suspect/dead/joins/leaves)
	TargetID         NodeID   // for indirect pings

}

func (m Message) String() string {
	return fmt.Sprintf("Received %v from %v with %v updates",
		m.Type, m.Sender, m.MembershipUpdate)
}

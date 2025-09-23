package main

// TODO: Fix heartbeat counter and how nodes that left are joined back
import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

var membershipList map[NodeID]Member

// Hyperparamter
var DEFAULT_DISSEMINATE int = 5

func main() {
	// Command Line Arguments
	ip := flag.String("ip", "127.0.0.1", "VM hostname")
	port := flag.Int("port", 5001, "Port to bind")
	introducer := flag.String("introducer", "", "IP:port of introducer (leave blank if current VM is introducer)")
	// mode := flag.String("mode", "gossip", "Gossip or SWIM")
	flag.Parse()

	membershipList = make(map[NodeID]Member)
	version, err := getVersion()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	currNodeID := NodeID{IP: *ip, Port: *port, Version: version}

	membershipList[currNodeID] = Member{
		ID:          currNodeID,
		State:       Alive,
		Heartbeat:   1,
		LastUpdate:  time.Now(),
		Disseminate: 0,
	}
	fmt.Printf("HERE: %v\n", currNodeID)

	// Open UDP socket and listen for any writes to the socket
	addr := net.UDPAddr{IP: net.ParseIP(currNodeID.IP), Port: currNodeID.Port}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		fmt.Println("Error binding UDP:", err)
	}
	defer conn.Close()

	if *introducer != "" {
		host, portStr, _ := net.SplitHostPort(*introducer)
		port, _ := strconv.Atoi(portStr)
		// temporary version of the introducer, will be replaced in the 'join_ack' due to updateMembershipList(), if it's still -1, it's an error
		temporaryVersion := -1
		introducerNode := NodeID{IP: host, Port: port, Version: temporaryVersion}

		// send a join request to the introducer
		msg := Message{
			Type:            "join",
			SenderID:        currNodeID,
			SenderState:     Alive,
			SenderHeartbeat: 1,
		}

		data, err := json.Marshal(msg)
		if err != nil {
			fmt.Printf("Error handling Marshaling JSON: %v", err)
		}

		err = sendUDPto(introducerNode, data, conn)
		if err != nil {
			fmt.Printf("Failed to send UDP packet: %v", err)
		} else {
			fmt.Printf("Sent join request to introducer %v\n", introducerNode)
		}
	} else {
		fmt.Println("I am the introducer")
	}

	// Start receiver loop in the background
	go recvLoop(conn)

	// Start gossip loop in the background
	go gossipLoop(currNodeID, conn)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// runs in the background, listening for interrupt or terminated signals, once it receives a signal, call leaveGroup()
	<-sigChan
	leaveGroup(currNodeID, conn)
}

// recvLoop listens for incoming messages and logs them
func recvLoop(conn *net.UDPConn) {
	buf := make([]byte, 4096)
	for {
		fmt.Println("------------------------------------------------------")
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Printf("recv error at address: %v: %v\n", addr, err)
			continue
		}

		var msg Message
		err = json.Unmarshal(buf[:n], &msg)
		if err != nil {
			fmt.Println("unmarshal error:", err)
			continue
		}
		fmt.Printf("Received %s from Member: %v\nHeartbeat:%d\nState: %s\n", msg.Type, msg.SenderID, msg.SenderHeartbeat, msg.SenderState)

		// Join and Leave handling
		// Join: Only introducer and new node should use these messages
		// Leave: Leaving node sends it to a random node in the group
		switch msg.Type {
		// TODO: Fix this
		case "join":

			// Add the new node to introducerNode's table
			membershipList[msg.SenderID] = Member{
				ID:          msg.SenderID,
				State:       Alive,
				Heartbeat:   1,
				LastUpdate:  time.Now(),
				Disseminate: DEFAULT_DISSEMINATE,
			}

			// Send back the full table of introducerNode
			fullTable := []Member{}
			for _, member := range membershipList {
				fullTable = append(fullTable, member)
			}

			// Send an ack to the new node, note that we don't add SenderID: introducerNode.ID as it's part of fullTable
			ack := Message{
				Type:             "join_ack",
				MembershipUpdate: fullTable,
			}
			fmt.Printf("FULLTABLE: %v", fullTable)
			data, _ := json.Marshal(ack)
			err := sendUDPto(msg.SenderID, data, conn)
			if err != nil {
				fmt.Printf("Failed to send UDP packet: %v", err)
			}

			// peer, ok := getRandomPeer()

		case "join_ack":
			// Add the fullTable to the currNode's table
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		case "leave":
			fmt.Printf("Node %v leaving\n", msg.SenderID)
			m := membershipList[msg.SenderID]
			m.State = Dead
			m.LastUpdate = time.Now()
			m.Disseminate = 3
			membershipList[msg.SenderID] = m
		case "gossip":
			// Refresh the sender node in currNode's Membership List as we got a message from them
			membershipList[msg.SenderID] = Member{
				ID:         msg.SenderID,
				State:      Alive,
				Heartbeat:  msg.SenderHeartbeat + 1,
				LastUpdate: time.Now(),
			}

			// Merge piggybacked updates (combine with join_ack?)
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		}

	}
}

func updateMembershipList(newNode Member) {
	oldNode, ok := membershipList[newNode.ID]

	// update the entry in table if it doesn't exist or the incarnation value is lower
	if !ok || newNode.ID.Version > oldNode.ID.Version {
		membershipList[newNode.ID] = newNode
		fmt.Printf("Updated %v to %v\nState: %s\nVersion: %d \n", oldNode.ID, newNode.ID, newNode.State, newNode.ID.Version)
		return
	}

	// if the versions are the same, we compare the heartbeats or states and take according to (alive < suspect < dead)
	if newNode.ID.Version == oldNode.ID.Version {
		if (newNode.Heartbeat > oldNode.Heartbeat) || (newNode.Heartbeat == oldNode.Heartbeat && newNode.State > oldNode.State) {
			if newNode.Heartbeat > oldNode.Heartbeat {
				fmt.Printf("Merged %v into %v, because of larger heartbeat %d > %d\n", newNode.ID, oldNode.ID, newNode.Heartbeat, oldNode.Heartbeat)
			}
			if newNode.Heartbeat == oldNode.Heartbeat && rank(newNode.State) > rank(oldNode.State) {
				fmt.Printf("Merged %v into %v, because of higher state %d > %d\n", newNode.ID, oldNode.ID, newNode.State, oldNode.State)
			}

			if rank(newNode.State) > rank(oldNode.State) {
				// if the state is different, we update the TTL because a state change should be gossiped through the MembershipUpdate (piggybacking on heartbeats/acks)
				newNode.Disseminate = DEFAULT_DISSEMINATE
			}
			membershipList[newNode.ID] = newNode
		}
	}
	// TODO:

	// if newNode.State == Dead {
	//
	// }
}

func rank(state NodeState) int {
	switch state {
	case (Alive):
		return 1
	case (Suspect):
		return 2
	case (Dead):
		return 3
	default:
		return 0
	}
}

func sendUDPto(receiverNode NodeID, data []byte, conn *net.UDPConn) error {
	addr := net.UDPAddr{IP: net.ParseIP(receiverNode.IP), Port: receiverNode.Port}
	_, err := conn.WriteToUDP(data, &addr)
	if err != nil {
		return fmt.Errorf("send error: %e", err)
	} else {
		fmt.Printf("Sent gossip to %v:%d\n", receiverNode.IP, receiverNode.Port)
		return nil
	}
}

func gossipLoop(currNode NodeID, conn *net.UDPConn) {
	ticker := time.NewTicker(1 * time.Second)
	for range ticker.C {
		peer, err := getRandomNode(currNode)
		if err != nil {
			fmt.Printf("error: %v", err)
			continue
		}
		changedNodes := getChangedNodes()

		msg := Message{
			Type:             "gossip",
			SenderID:         currNode,
			SenderState:      Alive,
			SenderHeartbeat:  membershipList[peer].Heartbeat,
			MembershipUpdate: changedNodes,
		}
		data, _ := json.Marshal(msg)
		err = sendUDPto(peer, data, conn)
		if err != nil {
			fmt.Printf("Failed to send UDP packet: %v", err)
		}
	}
}

// Gossip any nodes whose State has changed (will have a positive Disseminate counter)
func getChangedNodes() []Member {
	changedNodes := []Member{}

	for id, member := range membershipList {
		if member.Disseminate > 0 {
			member.Disseminate--
			changedNodes = append(changedNodes, member)
			membershipList[id] = member
		}
	}
	return changedNodes
}

func getRandomNode(currNodeID NodeID) (NodeID, error) {
	candidates := []NodeID{}
	for id, m := range membershipList {
		if id == currNodeID || m.State == Dead {
			continue
		}
		candidates = append(candidates, id)
	}

	if len(candidates) == 0 {
		return NodeID{}, fmt.Errorf("Could not find a random peer\n")
	}
	return candidates[rand.Intn(len(candidates))], nil
}

func leaveGroup(currNode NodeID, conn *net.UDPConn) {
	leaveMsg := Message{
		Type:        "leave",
		SenderID:    currNode,
		SenderState: Dead,
	}
	data, _ := json.Marshal(leaveMsg)

	peer, err := getRandomNode(currNode)
	if err != nil {
		fmt.Printf("error: %v", err)
	} else {
		err = sendUDPto(peer, data, conn)
		if err != nil {
			fmt.Printf("Failed to send UDP packet: %v", err)
		}

		fmt.Printf("Sent leave message to %v, shutting down: %v\n", peer, currNode)
	}

	// Found online of how to flush stdout before exiting
	os.Stdout.Sync()
	os.Exit(0)
}

func getVersion() (int, error) {
	// hostname, err := os.Hostname()
	// if err != nil {
	// 	fmt.Printf("Could not find hostname\n")
	// }
	// vmNumber, err := getMachineNumber(hostname)
	// if err != nil {
	// 	fmt.Printf("Could not find machine number\n")
	// 	return 0, err
	// }

	// TODO: Test on local machine for now
	vmNumber := 0

	// TODO: Changed this back to /home/shared/incarnation_%d.log for testing on vms
	filePath := fmt.Sprintf("./incarnation_%d.log", vmNumber)
	data, err := os.ReadFile(filePath)
	if err != nil {
		// file doesn't exist yet, 0644 is the permission to create the file
		os.WriteFile(filePath, []byte("1"), 0644)
		return 1, nil
	}

	v, err := strconv.Atoi(string(data))
	if err != nil {
		fmt.Printf("Could not parse '%s': %v\n", filePath, err)
		fmt.Printf("Defaulting to 1...")
		v = 0
	}
	// increment for this incarnation of the vm
	v++

	os.WriteFile(filePath, []byte(strconv.Itoa(v)), 0644)
	return v, nil
}

// ex: input fa25-cs425-a901.cs.illinois.edu returns 1
func getMachineNumber(hostname string) (int, error) {
	re := regexp.MustCompile(`-(\d{3})\.`)
	match := re.FindStringSubmatch(hostname)
	if len(match) < 2 {
		return 0, fmt.Errorf("no machine number found in hostname: %s", hostname)
	}

	num, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, err
	}

	return num - 900, nil
}

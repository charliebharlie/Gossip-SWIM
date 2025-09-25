package main

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
	"sync"
	"syscall"
	"time"
)

var membershipList map[NodeID]Member
var mu sync.RWMutex
var suspicionTimers map[NodeID]chan struct{}

// Hyperparamters
var delta int = 1
var DEFAULT_DISSEMINATE int = 5
var T_Fail = time.Duration(1*delta) * time.Second
var T_Suspicion = time.Duration(2*delta) * time.Second

func main() {
	// print(time.Duration(T_Fail)/time.Second, " ", time.Duration(T_Suspicion)/time.Second)
	// os.Exit(0)

	// Command Line Arguments
	ip := flag.String("ip", "127.0.0.1", "VM hostname")
	port := flag.Int("port", 5001, "Port to bind")
	introducer := flag.String("introducer", "", "IP:port of introducer (leave blank if current VM is introducer)")
	// mode := flag.String("mode", "gossip", "Gossip or SWIM")
	flag.Parse()

	membershipList = make(map[NodeID]Member)
	suspicionTimers = make(map[NodeID]chan struct{})

	version, err := getVersion()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	currNodeID := NodeID{IP: *ip, Port: *port}

	membershipList[currNodeID] = Member{
		ID:          currNodeID,
		Version:     version,
		State:       Alive,
		Heartbeat:   1,
		LastUpdate:  time.Now(),
		Disseminate: 0,
	}
	fmt.Printf("HERE: %v\n", membershipList[currNodeID])

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
		introducerNode := NodeID{IP: host, Port: port}

		// send a join request to the introducer
		msg := Message{
			Type:             "join",
			Sender:           membershipList[currNodeID],
			MembershipUpdate: []Member{},
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
	go recvLoop(conn, currNodeID)

	// Start gossip loop in the background
	go gossipLoop(currNodeID, conn)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT)

	// runs in the background, listening for interrupt signals, once it receives a signal, voluntarily leave the group
	<-sigChan
	leaveGroup(currNodeID, conn)
}

// recvLoop listens for incoming messages and logs them
func recvLoop(conn *net.UDPConn, currNodeID NodeID) {
	buf := make([]byte, 4096)
	for {
		fmt.Println("------------------------------------------------------------------------------")
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Printf("recv error: %v at address: %v\n", err, addr)
			continue
		}

		var msg Message
		err = json.Unmarshal(buf[:n], &msg)
		if err != nil {
			fmt.Println("unmarshal error:", err)
			continue
		}

		fmt.Println(msg)
		fmt.Println("----------- BEFORE -----------")
		fmt.Println("Current Table: ")
		for _, member := range membershipList {
			fmt.Println(member)
		}

		// Join and Leave handling
		// Join: Only introducer and new node should use these messages
		// Leave: Leaving node sends it to any random node in the group
		switch msg.Type {
		case "join":
			mu.Lock()

			// Add the new node to introducerNode's table
			membershipList[msg.Sender.ID] = Member{
				ID:          msg.Sender.ID,
				Version:     msg.Sender.Version,
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

			ack := Message{
				Type:             "join_ack",
				Sender:           membershipList[currNodeID],
				MembershipUpdate: fullTable,
			}

			data, _ := json.Marshal(ack)
			err := sendUDPto(msg.Sender.ID, data, conn)
			if err != nil {
				fmt.Printf("Failed to send UDP packet: %v", err)
			}
			mu.Unlock()

		case "join_ack":
			// Add the fullTable to the newNode's table
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		case "leave":
			mu.Lock()
			fmt.Printf("Node %v leaving\n", msg.Sender.ID)
			currNode := membershipList[msg.Sender.ID]
			currNode.State = Dead
			currNode.LastUpdate = time.Now()
			currNode.Disseminate = DEFAULT_DISSEMINATE
			membershipList[msg.Sender.ID] = currNode
			mu.Unlock()

		case "gossip":
			// Refresh the sender node's entry in currNode's Membership List as we got a message from them
			updateMembershipList(msg.Sender)

			// Merge piggybacked updates
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

			refreshSuspicionTimer(msg.Sender.ID)
		}

		fmt.Println("----------- AFTER -----------")
		// fmt.Println(msg.Sender)

		fmt.Println("Current Table: ")
		for _, member := range membershipList {
			fmt.Println(member)
		}

	}
}

func updateMembershipList(newNode Member) {
	mu.Lock()
	defer mu.Unlock()
	oldNode, exists := membershipList[newNode.ID]

	// update the entry in membershipList if it doesn't exist or the version is lower
	if !exists || newNode.Version > oldNode.Version {
		newNode.LastUpdate = time.Now()
		membershipList[newNode.ID] = newNode
		fmt.Printf("Updated %v to %v\nState: %s\nVersion: %d \n", oldNode.ID, newNode.ID, newNode.State, newNode.Version)
		return
	}

	// if the versions are the same, we compare the heartbeats or states and take according to (alive < suspect < dead). TODO: If the newNode entry's State is Dead, we accept it no matter what. <--- Check this (not sure why, the system works much better for NOT taking Dead state nodes no matter what [ie. we consider a larger Heartbeat value instead])
	if newNode.Version == oldNode.Version {
		if (newNode.Heartbeat > oldNode.Heartbeat) || (newNode.Heartbeat == oldNode.Heartbeat && rank(newNode.State) > rank(oldNode.State)) {
			if newNode.Heartbeat > oldNode.Heartbeat {
				fmt.Printf("Heartbeat Merged %v into %v\n", newNode, oldNode)
			}
			if newNode.Heartbeat == oldNode.Heartbeat && rank(newNode.State) > rank(oldNode.State) {
				fmt.Printf("State Merged %v into %v\n", newNode, oldNode)
			}

			if rank(newNode.State) != rank(oldNode.State) {
				// if the state is different, we update the TTL because a state change should be gossiped through the MembershipUpdate (piggybacking on heartbeats/acks)
				newNode.Disseminate = DEFAULT_DISSEMINATE
			}
			newNode.LastUpdate = time.Now()
			membershipList[newNode.ID] = newNode
		} else {
			oldNode.LastUpdate = time.Now()
			membershipList[newNode.ID] = oldNode
		}

	}
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
		return -1
	}
}

func refreshSuspicionTimer(currNodeID NodeID) {
	mu.Lock()
	defer mu.Unlock()

	// If the stop channel already exists, that means we should reset the timer (the timer being either the timer before suspicion or the timer after suspicion) as we heard from the suspected node
	stopChannel, exists := suspicionTimers[currNodeID]
	if exists {
		close(stopChannel)
		delete(suspicionTimers, currNodeID)
	}

	// We want to interrupt the timers if the channel is closed (ie. we received a heartbeat from the suspected node)
	stop := make(chan struct{})
	suspicionTimers[currNodeID] = stop

	go func(currNodeID NodeID, stop <-chan struct{}) {
		if membershipList[currNodeID].State == Alive {
			select {
			case <-time.After(T_Fail):
				mu.Lock()
				currNode := membershipList[currNodeID]
				currNode.State = Suspect
				currNode.Disseminate = DEFAULT_DISSEMINATE
				membershipList[currNodeID] = currNode
				fmt.Printf("Node %v is suspected\n", currNodeID)
				mu.Unlock()
			case <-stop:
				return
			}
		}

		if membershipList[currNodeID].State == Suspect {
			select {
			case <-time.After(T_Suspicion):
				mu.Lock()
				currNode := membershipList[currNodeID]
				currNode.State = Dead
				currNode.Disseminate = DEFAULT_DISSEMINATE
				membershipList[currNodeID] = currNode
				fmt.Printf("Node %v is dead\n", currNodeID)
				mu.Unlock()
			case <-stop:
				return
			}
		}
	}(currNodeID, stop)
}

func sendUDPto(receiverNode NodeID, data []byte, conn *net.UDPConn) error {
	addr := net.UDPAddr{IP: net.ParseIP(receiverNode.IP), Port: receiverNode.Port}
	_, err := conn.WriteToUDP(data, &addr)
	if err != nil {
		return fmt.Errorf("send error: %e", err)
	} else {
		fmt.Printf("Sent gossip to %v\n", receiverNode)
		return nil
	}
}

func gossipLoop(currNodeID NodeID, conn *net.UDPConn) {
	// Tuning how often the nodes should gossip
	ticker := time.NewTicker(time.Duration(1000*delta) * time.Millisecond)
	for range ticker.C {
		mu.Lock()
		currNode := membershipList[currNodeID]
		currNode.Heartbeat++
		membershipList[currNodeID] = currNode
		mu.Unlock()

		peer, err := getRandomNode(currNodeID)
		if err != nil {
			fmt.Printf("error: %v", err)
			continue
		}

		// piggyback any changes with the normal heartbeat
		piggyBackNodes := getPiggyBackNodes()

		msg := Message{
			Type:             "gossip",
			Sender:           membershipList[currNodeID],
			MembershipUpdate: piggyBackNodes,
		}
		data, _ := json.Marshal(msg)
		err = sendUDPto(peer, data, conn)
		if err != nil {
			fmt.Printf("Failed to send UDP packet: %v", err)
		}
	}
}

// Gossip any nodes whose State has changed or any new nodes (will have a positive Disseminate counter)
func getPiggyBackNodes() []Member {
	mu.Lock()
	defer mu.Unlock()

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
	mu.RLock()
	defer mu.RUnlock()

	candidates := []NodeID{}
	for id, member := range membershipList {
		if id == currNodeID || member.State == Dead {
			continue
		}

		candidates = append(candidates, id)
	}

	if len(candidates) == 0 {
		return NodeID{}, fmt.Errorf("Could not find a random node\n")
	}

	return candidates[rand.Intn(len(candidates))], nil
}

// voluntarily leaves the group
func leaveGroup(currNode NodeID, conn *net.UDPConn) {
	mu.RLock()
	defer mu.RUnlock()

	leaveMsg := Message{
		Type:   "leave",
		Sender: membershipList[currNode],
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

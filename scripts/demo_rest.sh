hosts=(
	fa25-cs425-a901.cs.illinois.edu
	fa25-cs425-a902.cs.illinois.edu
	fa25-cs425-a903.cs.illinois.edu
	fa25-cs425-a904.cs.illinois.edu
	fa25-cs425-a905.cs.illinois.edu
	fa25-cs425-a906.cs.illinois.edu
	fa25-cs425-a907.cs.illinois.edu
	fa25-cs425-a908.cs.illinois.edu
	fa25-cs425-a909.cs.illinois.edu
	fa25-cs425-a910.cs.illinois.edu
)

read -p "Enter introducer machine number (1-10): " INTRO_NUM
index=$((INTRO_NUM - 1))
introducer=${hosts[$index]}
echo "Chosen introducer: $introducer"

# start 4 nodes
for host in "${hosts[@]:1:4}"; do
	echo "Starting node on $host..."
	LOGFILE="/home/cliu132/mp2/logs/server_${host}.log"

	ssh -i ~/.ssh/id_ed25520 cliu132@$host bash -s <<EOF
cd ~/mp2
mkdir -p ~/mp2/logs
nohup go run . --ip=$host --introducer=$introducer:5003 --dropRate=0 > $LOGFILE 2>&1 &
EOF

done

# wait 20s
echo "Waiting 20 seconds..."
# sleep 20

# start the remaining nodes
for host in "${hosts[@]:5}"; do
	echo "Starting node on $host..."
	LOGFILE="/home/cliu132/mp2/logs/server_${host}.log"

	ssh -i ~/.ssh/id_ed25520 cliu132@$host bash -s <<EOF
cd ~/mp2
mkdir -p ~/mp2/logs
nohup go run . --ip=$host --introducer=$introducer:5003 --dropRate=0 > $LOGFILE 2>&1 &
EOF

done

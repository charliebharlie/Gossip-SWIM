#!/bin/bash

# List of machines
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

for host in "${hosts[@]}"; do
	echo "Starting node on $host..."

	ssh -i ~/.ssh/id_ed25520 cliu132@"$host" bash -s <<EOF
pids=\$(lsof -t -i :5003)
if [ -n "\$pids" ]; then
  echo "Killing processes on port 5003: \$pids"
  kill -9 \$pids
else
  echo "No process using port 5003"
fi

cd ~/mp2

mkdir -p /home/cliu132/mp2/logs
LOGFILE="/home/cliu132/mp2/logs/server_$host.log"
touch /home/cliu132/mp2/incarnation_$host.log

if [ "$host" = "$introducer" ]; then
    echo "Starting introducer on $host"
    nohup go run . --ip=$host --drop-rate=0 > \$LOGFILE 2>&1 &
else
    echo "Starting node on $host (introducer=$introducer)"
    nohup go run . --ip=$host --introducer=$introducer:5003 --drop-rate=0  > \$LOGFILE 2>&1 &
fi
EOF
done

# echo "Waiting 10 seconds..."
# sleep 60
#
# # Clean up the nodes
# for host in "${hosts[@]}"; do
# 	echo "Stopping node on $host..."
# 	ssh -i ~/.ssh/id_ed25520 cliu132@"$host" "pids=\$(lsof -t -i :5003); [ -n \"\$pids\" ] && kill -SIGINT \$pids"
# done
#
# echo "Killed all processes on nodes, check ~/mp2/logs for the log of the vm's run"

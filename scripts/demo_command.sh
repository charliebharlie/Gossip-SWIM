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
read -p "Enter machine number (1-10): " num
index=$((num - 1))
host=${hosts[$index]}
echo "Chosen vm: $host"
read -p "Enter command: " command
echo "Command: $host"

ssh -t -i ~/.ssh/id_ed25520 cliu132@$host "echo '$command' | nc localhost 9000"

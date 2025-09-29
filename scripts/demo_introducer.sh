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

# start introducer
ssh -t -i ~/.ssh/id_ed25520 cliu132@$introducer "cd ~/mp2 && go run . --ip=$introducer --drop-rate=0"

#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

case "$1" in
    up)
        echo "Building Slurm node base image..."
        docker compose build
        echo "Starting Slurm multi-node cluster..."
        docker compose up -d
        echo "Waiting for services to initialize..."
        sleep 8
        docker compose exec slurmctld sinfo || true
        ;;
    down)
        echo "Stopping Slurm multi-node cluster..."
        docker compose down -v
        ;;
    status)
        echo "=== Slurm Node & Cluster Status (sinfo) ==="
        docker compose exec slurmctld sinfo
        echo "=== Slurm Queue (squeue) ==="
        docker compose exec slurmctld squeue
        ;;
    token)
        USER_NAME="${2:-hpcuser}"
        echo "Generating JWT Token for user: ${USER_NAME}..."
        docker compose exec slurmctld scontrol token username=${USER_NAME} lifespan=86400
        ;;
    login)
        echo "Logging in to Slurm Master Node (slurmctld)..."
        docker compose exec -it slurmctld /bin/bash
        ;;
    test)
        echo "=== Executing Slurm Multi-Node Test Suite ==="
        echo "1. Cluster Node Info:"
        docker compose exec slurmctld sinfo
        
        echo "2. Multi-node hostname execution (srun -N3 -n3 hostname):"
        docker compose exec slurmctld srun -N3 -n3 hostname
        
        echo "3. Submitting batch job (sbatch):"
        docker compose exec slurmctld bash -c "cat <<'EOF' > /shared/test_job.sh
#!/bin/bash
#SBATCH --job-name=test-multi-node
#SBATCH --nodes=2
#SBATCH --ntasks=4
#SBATCH --output=/shared/test_job_%j.out

echo 'Starting multi-node job on nodes: \$SLURM_JOB_NODELIST'
srun hostname
sleep 2
echo 'Job finished.'
EOF
sbatch /shared/test_job.sh"
        
        echo "4. Checking queue status (squeue):"
        sleep 1
        docker compose exec slurmctld squeue
        
        echo "5. Waiting for job completion and checking logs:"
        sleep 5
        docker compose exec slurmctld cat /shared/test_job_*.out || true
        
        echo "6. Testing SlurmRESTd API Endpoints:"
        TOKEN_RAW=$(docker compose exec slurmctld scontrol token username=hpcuser lifespan=3600 | grep 'SLURM_JWT=' | cut -d'=' -f2 | tr -d '\r')
        echo "Generated JWT Token for slurmrestd test."
        
        echo "[API PING via JWT Auth]:"
        curl -s -H "X-SLURM-USER-NAME: hpcuser" -H "X-SLURM-USER-TOKEN: ${TOKEN_RAW}" http://localhost:6820/slurm/v0.0.37/ping || true
        echo ""
        echo "[API NODES SUMMARY via JWT Auth]:"
        curl -s -H "X-SLURM-USER-NAME: hpcuser" -H "X-SLURM-USER-TOKEN: ${TOKEN_RAW}" http://localhost:6820/slurm/v0.0.37/nodes | head -n 25 || true
        echo ""
        ;;
    *)
        echo "Usage: $0 {up|down|status|token [username]|login|test}"
        exit 1
        ;;
esac

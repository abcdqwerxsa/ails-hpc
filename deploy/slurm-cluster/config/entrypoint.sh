#!/bin/bash
set -e

mkdir -p /etc/munge /var/log/munge /var/lib/munge /var/run/munge /shared
chown -R munge:munge /etc/munge /var/log/munge /var/lib/munge /var/run/munge
chmod 0755 /var/run/munge /var/lib/munge /var/log/munge /etc/munge

# 如果 /shared/munge.key 不存在，则创建共享 munge key
if [ ! -f /shared/munge.key ]; then
    echo "Creating shared munge.key in /shared..."
    dd if=/dev/urandom bs=1 count=1024 of=/shared/munge.key 2>/dev/null
    chown munge:munge /shared/munge.key
    chmod 400 /shared/munge.key
fi

# 复制密钥并设置私有权限
cp -f /shared/munge.key /etc/munge/munge.key
chown munge:munge /etc/munge/munge.key
chmod 400 /etc/munge/munge.key

# 启动 Munge 认证守护进程
gosu munge /usr/sbin/munged --force

echo "Munge service started on $(hostname)."

# 从挂载模版自动构建专属 600 权限的 /etc/slurm/slurmdbd.conf
if [ -f /etc/slurm/slurmdbd.conf.template ]; then
    echo "Generating /etc/slurm/slurmdbd.conf with 600 permissions from template..."
    cp -f /etc/slurm/slurmdbd.conf.template /etc/slurm/slurmdbd.conf
    chown slurm:slurm /etc/slurm/slurmdbd.conf
    chmod 600 /etc/slurm/slurmdbd.conf
fi

# 等待端口就绪的辅助方法
wait_for_port() {
    local host=$1
    local port=$2
    local service_name=$3
    echo "Waiting for ${service_name} (${host}:${port})..."
    while ! nc -z "${host}" "${port}"; do
        sleep 1
    done
    echo "${service_name} is ready."
}

chown -R slurm:slurm /var/spool/slurmctld /var/spool/slurmd /var/log/slurm /var/run/slurm

case "$ROLE" in
    slurmdbd)
        wait_for_port "slurm-db" "3306" "MariaDB"
        echo "Starting slurmdbd..."
        exec gosu slurm /usr/sbin/slurmdbd -D -v
        ;;

    slurmctld)
        wait_for_port "slurmdbd" "6819" "SlurmDBD"
        sleep 2

        # 创建并初始化属组可读 (640) 的 JWT Symmetric Key
        if [ ! -f /etc/slurm/jwt_hs256.key ]; then
            echo "Generating JWT Key in /etc/slurm/jwt_hs256.key..."
            dd if=/dev/urandom bs=1 count=32 of=/etc/slurm/jwt_hs256.key 2>/dev/null
            chown slurm:slurm /etc/slurm/jwt_hs256.key
            chmod 640 /etc/slurm/jwt_hs256.key
        fi
        
        # 尝试注册 Cluster 到 SlurmDBD 记账服务中
        echo "Registering cluster ails-hpc-cluster in SlurmDBD..."
        gosu slurm sacctmgr -i add cluster ails-hpc-cluster || true

        echo "Starting slurmctld..."
        gosu slurm /usr/sbin/slurmctld -v
        sleep 2

        echo "Starting slurmrestd API daemon with SLURM_JWT=daemon on port 6820..."
        export SLURM_JWT=daemon
        gosu hpcuser /usr/sbin/slurmrestd -a rest_auth/jwt -f /etc/slurm/slurm.conf 0.0.0.0:6820 &
        
        echo "Slurmctld and SlurmRESTd services initialized successfully."
        exec tail -f /var/log/slurm/slurmctld.log
        ;;

    slurmd)
        wait_for_port "slurmctld" "6817" "Slurmctld"
        echo "Starting slurmd on node $(hostname)..."
        exec /usr/sbin/slurmd -D -v
        ;;

    *)
        echo "No ROLE specified or unknown ROLE: '$ROLE'. Running interactive bash shell."
        exec /bin/bash
        ;;
esac

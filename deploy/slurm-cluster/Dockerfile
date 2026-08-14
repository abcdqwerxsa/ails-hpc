FROM docker.m.daocloud.io/library/ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# 替换 APT 源为中科大 USTC 镜像源加速下载
RUN sed -i 's/archive.ubuntu.com/mirrors.ustc.edu.cn/g' /etc/apt/sources.list && \
    sed -i 's/security.ubuntu.com/mirrors.ustc.edu.cn/g' /etc/apt/sources.list

# 安装 Slurm, SlurmDBD, SlurmRESTd, Munge 及集群基础工具
RUN apt-get update && apt-get install -y \
    slurm-wlm \
    slurmdbd \
    slurmrestd \
    munge \
    libmunge-dev \
    mariadb-client \
    openmpi-bin \
    libopenmpi-dev \
    gcc \
    g++ \
    gfortran \
    make \
    cmake \
    sudo \
    curl \
    wget \
    vim \
    git \
    net-tools \
    iputils-ping \
    psmisc \
    gosu \
    netcat \
    && rm -rf /var/lib/apt/lists/*

# 交互式 Web IDE/Notebook 运行时（OOD 范式：作为 Slurm 作业在计算节点上由 sbatch 拉起）
#   - jupyterlab/notebook：清华 pip 镜像
#   - code-server：官方 install.sh（从 GitHub release 下载）
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-pip && \
    pip3 install --no-cache-dir \
        -i https://pypi.tuna.tsinghua.edu.cn/simple \
        jupyterlab notebook && \
    curl -fsSL https://code-server.dev/install.sh | sh && \
    rm -rf /var/lib/apt/lists/*

# 配置 Munge 和 Slurm 系统账号与 PID/Spool 专属目录
RUN groupadd -g 991 munge && \
    useradd -u 991 -g munge -d /var/lib/munge -s /bin/false munge || true

RUN groupadd -g 64030 slurm && \
    useradd -u 64030 -g slurm -d /var/lib/slurm -s /bin/bash slurm || true

RUN groupadd -g 1001 hpcuser && \
    useradd -u 1001 -g hpcuser -m -s /bin/bash hpcuser && \
    usermod -aG slurm hpcuser && \
    echo "hpcuser:hpcuser" | chpasswd && \
    echo "hpcuser ALL=(ALL) NOPASSWD: ALL" >> /etc/sudoers

RUN mkdir -p /var/spool/slurmctld /var/spool/slurmd /var/log/slurm /var/run/slurm /shared /etc/slurm \
    && chown -R slurm:slurm /var/spool/slurmctld /var/spool/slurmd /var/log/slurm /var/run/slurm /etc/slurm \
    && mkdir -p /etc/munge /var/log/munge /var/lib/munge /var/run/munge \
    && chown -R munge:munge /etc/munge /var/log/munge /var/lib/munge /var/run/munge

WORKDIR /shared

COPY config/slurm.conf /etc/slurm/slurm.conf
COPY config/slurmdbd.conf /etc/slurm/slurmdbd.conf
COPY config/entrypoint.sh /entrypoint.sh

RUN chmod 644 /etc/slurm/slurm.conf && \
    chmod 600 /etc/slurm/slurmdbd.conf && \
    chown slurm:slurm /etc/slurm/slurm.conf /etc/slurm/slurmdbd.conf && \
    chmod +x /entrypoint.sh

EXPOSE 6817 6818 6819 6820

ENTRYPOINT ["/entrypoint.sh"]

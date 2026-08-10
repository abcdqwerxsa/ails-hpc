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

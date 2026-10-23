#!/bin/bash
# S5-T7 constrained replay (IMPLEMENTED, NOT RUN by the S5 agent; lead runs alone).
# Real-time 5 Hz stream, 2 cores (taskset), 4 GB address-space cap, 1000..5000 particles; frames arriving while busy are dropped.
# Usage: bash solutions/s5_swa_pf/constrained_replay.sh [particles=2000]
cd "$(dirname "$0")"; source env/bin/activate
N=${1:-2000}
export OMP_NUM_THREADS=2
taskset -c 0,1 prlimit --as=6000000000 python run_s5.py --run-id S5-T7-$(date +%Y%m%d)-s0 --n $N --stride 1 --realtime --seed 0

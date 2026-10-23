#!/bin/bash
# Sequential S6 test chain (offline accuracy tests). Latency/real-time/T5 are run separately ALONE by the lead.
cd "$(dirname "$0")"; source env/bin/activate; export OMP_NUM_THREADS=2
d=20261023
run(){ echo "$(date +%T) start $*" >> /tmp/s6_chain.log; nice "$@" >> /tmp/s6_chain.log 2>&1; echo "$(date +%T) done $1 $2 $3" >> /tmp/s6_chain.log; }
run python driver.py --run-id S6-T1-$d-s0 --stride 1
run python driver.py --run-id S6-T4off-$d-s0 --stride 1 --cfg '{"tracking":true}'
run python driver.py --run-id S6-T3-xfeatlg-$d-s0 --stride 5
run python driver.py --run-id S6-T3-splg-$d-s0 --stride 5 --cfg '{"matcher":"splg"}'
run python t2_retriever.py --retr megaloc --run-id S6-T2-megaloc-$d-s0
run python t2_retriever.py --retr dinov2s --run-id S6-T2-dinov2s-$d-s0
echo ALLDONE >> /tmp/s6_chain.log

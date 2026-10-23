#!/bin/bash
# Sequential queue for S5 sweeps (nadir frames, 1 Hz). Run after T2b. nice, 2 threads.
cd "$(dirname "$0")"; source env/bin/activate; export OMP_NUM_THREADS=2
R=../../results/s5/nidar; D=20261023
while [ ! -f $R/S5-T2-$D-s0/metrics.json ]; do sleep 20; done
for n in 1000 5000 20000 40000; do
  nice python run_s5.py --run-id S5-T3-n$n-$D-s0 --n $n --max-frames 40 --seed 0 > /dev/null 2>&1
done
for seed in 0 1 2; do for init in 0 1 2; do
  nice python run_s5.py --run-id S5-T4-init$init-$D-s$seed --n 5000 --init $init --max-frames 40 --seed $seed > /dev/null 2>&1
done; done
nice python run_s5.py --run-id S5-T5-res1.0-$D-s0 --n 5000 --res 1.0 --max-frames 60 --seed 0 > /dev/null 2>&1
nice python run_s5.py --run-id S5-T5-res2.0-$D-s0 --n 5000 --res 2.0 --max-frames 60 --seed 0 > /dev/null 2>&1
echo done > $R/queue.done

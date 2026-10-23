#!/bin/bash
# Serial latency / real-time / constrained runs: one job at a time on an otherwise idle machine.
# Real-time: frames arrive at the native 5 Hz clock (153 s of flight); frames arriving while a step runs are dropped.
# Constrained: pinned with taskset to 1-2 cores (approximation of a Jetson-class CPU budget; NOT a Jetson).
cd /home/user/Nexmesh/gpsdenied-eval/solutions
D=$(date +%Y%m%d); L=/tmp/serial
run() { name=$1; shift; echo "START $name $(date +%T)" >> $L/progress.txt; "$@" > $L/$name.log 2>&1; echo "END $name rc=$? $(date +%T)" >> $L/progress.txt; }
# S6
cd s6_jetson_composite; source env/bin/activate; export OMP_NUM_THREADS=2
run s6_rt python driver.py --run-id S6-T4-rt-ser-$D-s0 --mode realtime --cfg '{"tracking":true}'
run s6_c2 taskset -c 0,1 python driver.py --run-id S6-T4-c2-ser-$D-s0 --mode realtime --cfg '{"tracking":true}'
run s6_lat python driver.py --run-id S6-T1lat-ser-$D-s0 --stride 5
run s6_t5 python t5_variants.py --run-id S6-T5-ser-$D-s0
deactivate; cd ..
# S3
cd s3_tile_matching; export S3_THREADS=2 OMP_NUM_THREADS=2
run s3_rt ./env/bin/python run_s3.py --tag S3-T6-rt-ser --mode realtime --step 1
run s3_c2 taskset -c 0,1 ./env/bin/python run_s3.py --tag S3-T6-c2-ser --mode realtime --step 1
run s3_lat ./env/bin/python run_s3.py --tag S3-T1lat-ser --step 5
cd ..
# S1
E=s1_gnss_denied_nav; export OMP_NUM_THREADS=2
run s1_rt $E/env/bin/python $E/driver.py --id S1-T3-ser --mode realtime --config '{"query_kp":1024,"tracking":true}'
run s1_t4 taskset -c 0,1 $E/env/bin/python $E/t4_constrained.py --id S1-T4-ser --threads 2
OMP_NUM_THREADS=1 run s1_c1 taskset -c 0 $E/env/bin/python $E/driver.py --id S1-T4rt-ser --mode realtime --config '{"query_kp":1024,"tracking":true}'
# S2
cd s2_uav_geolocalization; source env/bin/activate
OMP_NUM_THREADS=4 run s2_rt python run_s2.py t8 --mode realtime --threads 4
OMP_NUM_THREADS=1 run s2_c1 taskset -c 0 python run_s2.py t8 --mode constrained --cores 0 --threads 1
deactivate; cd ..
# S5
cd s5_swa_pf; source env/bin/activate; export OMP_NUM_THREADS=2
run s5_lat python run_s5.py --run-id S5-lat-ser-$D-s0 --n 20000 --stride 5 --max-frames 8
run s5_c2 bash constrained_replay.sh 2000
deactivate; cd ..
echo ALL_DONE >> $L/progress.txt

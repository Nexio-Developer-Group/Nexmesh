#!/bin/bash
# Sequential S1 test queue (CPU, nice). Common config: query_kp=1024 (CPU budget deviation, see ANALYSIS.md)
cd /home/user/Nexmesh/gpsdenied-eval
source solutions/s1_gnss_denied_nav/env/bin/activate
export OMP_NUM_THREADS=2
D="nice python solutions/s1_gnss_denied_nav/driver.py --date 20261023"
Q='"query_kp":1024'
$D --id S1-T1 --stride 5 --config "{$Q}" > /tmp/claude-0/T1.log 2>&1
$D --id S1-T2 --stride 5 --config "{$Q,\"tracking\":true}" > /tmp/claude-0/T2.log 2>&1
$D --id S1-T5 --stride 5 --config "{$Q,\"tracking\":true,\"gate\":false}" > /tmp/claude-0/T5.log 2>&1
$D --id S1-T9 --stride 5 --inject --config "{$Q,\"tracking\":true}" > /tmp/claude-0/T9.log 2>&1
$D --id S1-T2full --stride 1 --config "{$Q,\"tracking\":true}" > /tmp/claude-0/T2full.log 2>&1
$D --id S1-T6a --stride 10 --no-hints --config "{$Q,\"use_baro\":false,\"use_heading\":false}" > /tmp/claude-0/T6a.log 2>&1
$D --id S1-T6b --stride 10 --config "{$Q,\"use_heading\":false}" > /tmp/claude-0/T6b.log 2>&1

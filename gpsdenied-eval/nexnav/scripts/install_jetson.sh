#!/usr/bin/env bash
# nexnav installer for a Jetson (JetPack 5/6). NOT TESTED ON A JETSON in the evaluation (no hardware); read before running.
set -euo pipefail
echo "1) system packages (system OpenCV keeps GStreamer/CSI support; do NOT pip-install opencv-python on a Jetson)"
sudo apt-get update
sudo apt-get install -y python3-pip python3-venv python3-opencv python3-yaml python3-numpy git libopenblas-dev
echo "2) PyTorch: install the NVIDIA JetPack wheel that matches your JetPack (https://docs.nvidia.com/deeplearning/frameworks/install-pytorch-jetson-platform/)"
python3 -c "import torch;print('torch',torch.__version__,'cuda',torch.cuda.is_available())" || { echo "install the JetPack torch/torchvision wheels first, then re-run"; exit 1; }
echo "3) nexnav (use --system-site-packages so the venv sees the system OpenCV and the JetPack torch)"
python3 -m venv --system-site-packages ~/.nexnav/venv
source ~/.nexnav/venv/bin/activate
pip install --upgrade pip
pip install "$(dirname "$0")/.." pymavlink timm kornia einops tqdm scikit-learn pandas matplotlib pillow
echo "4) third-party code and weights (MegaLoc, XFeat, SWA-PF)"
nexnav setup --prefetch
echo "5) check"
nexnav config-template -o ~/nexnav.yaml
echo "Edit ~/nexnav.yaml (camera, map, mavlink), then:  nexnav check -c ~/nexnav.yaml  &&  nexnav run -c ~/nexnav.yaml"

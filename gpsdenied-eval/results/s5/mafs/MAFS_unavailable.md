# MAFS dataset: unavailable (S5-T1 and S5-T8 NOT RUN)
Attempts (2026-10-23):
1. README link https://pan.baidu.com/s/1mAgo7E3PUKMRWt2XhnNSNQ (pw 8888): `curl` -> "curl: (35) Recv failure: Connection reset by peer" (Baidu Pan unreachable).
2. Open-mirror search: WebSearch "MAFS dataset ... huggingface/zenodo/modelscope" -> only arXiv 2509.13795 pages; no mirror. API probes: huggingface.co/api/datasets?search=MAFS -> [] ; zenodo.org/api/records?q=MAFS UAV localization -> unrelated (UaVirBASE sound dataset); modelscope dataselect endpoint -> 404; GitHub search API -> blocked by the session proxy.
3. WebFetch of github.com/YuanJiayuuu/SWA-PF: the Baidu link is the only dataset link; no Releases, no alternative host, no LICENSE file. The README also says only part of the data is open.
Result: no MAFS data obtained. Nothing was invented; T1 (reproduction on a MAFS 200 m route) and T8 (cross-area) = NOT RUN.

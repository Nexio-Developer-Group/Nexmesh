# Adapter contract
class Adapter:
    def init(self, map_path: str, config: dict) -> None
    def step(self, frame: np.ndarray(BGR), timestamp: float, hints: dict|None) -> dict
step returns: {t, lat, lon, alt|None, yaw|None, conf: float, accepted: bool}
accepted=False when no fix (never a guess). hints: baro_alt, att (roll,pitch,yaw), prev (lat,lon).

hints = {baro_alt: m AGL, att: (roll, pitch, yaw) with yaw in DEGREES clockwise from north (roll=pitch=0: stabilised nadir camera), yaw_deg: same value}.
Frames are replayed in numeric time order. Camera: gimbal-stabilised nadir (image-up = heading). Harness note 2026-10-23: sort order and yaw units documented after agents found them; the offline replay result files are unaffected (time comes from the file name).

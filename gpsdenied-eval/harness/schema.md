# Adapter contract
class Adapter:
    def init(self, map_path: str, config: dict) -> None
    def step(self, frame: np.ndarray(BGR), timestamp: float, hints: dict|None) -> dict
step returns: {t, lat, lon, alt|None, yaw|None, conf: float, accepted: bool}
accepted=False when no fix (never a guess). hints: baro_alt, att (roll,pitch,yaw), prev (lat,lon).

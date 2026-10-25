from .base import Fix, Engine, EngineContext, resolve_device


def make_engine(name):
    if name == "s6":
        from .s6 import S6Engine; return S6Engine()
    if name == "s5":
        from .s5 import S5Engine; return S5Engine()
    if name == "dummy":
        from .dummy import DummyEngine; return DummyEngine()
    raise ValueError(f"unknown engine {name}")

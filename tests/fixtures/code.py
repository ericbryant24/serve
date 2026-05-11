def hello(name: str) -> str:
    """Say hello."""
    return f"Hello, {name}!"


class Greeter:
    def __init__(self, prefix: str = "Hi"):
        self.prefix = prefix

    def greet(self, name: str) -> str:
        return f"{self.prefix}, {name}!"

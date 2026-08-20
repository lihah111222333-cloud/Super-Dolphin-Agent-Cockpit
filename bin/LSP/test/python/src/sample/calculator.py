from sample.simple import add_one


def add_two(number: int) -> int:
    """Add two by reusing the package's one-step helper."""
    return add_one(add_one(number))


def total(values: list[int]) -> int:
    return sum(values)

import unittest

from sample.calculator import add_two, total


class TestCalculator(unittest.TestCase):
    def test_add_two(self):
        self.assertEqual(add_two(4), 6)

    def test_total(self):
        self.assertEqual(total([1, 2, 3]), 6)

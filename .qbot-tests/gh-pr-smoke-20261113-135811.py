import unittest


def add(a, b):
    return a + b


class AddTests(unittest.TestCase):
    def test_positive_numbers(self):
        self.assertEqual(add(2, 3), 5)

    def test_negative_numbers(self):
        self.assertEqual(add(-2, -3), -5)

    def test_mixed_signs(self):
        self.assertEqual(add(-2, 3), 1)

    def test_decimal_numbers(self):
        self.assertEqual(add(1.5, 2.25), 3.75)


if __name__ == "__main__":
    unittest.main()

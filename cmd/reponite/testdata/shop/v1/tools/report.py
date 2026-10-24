"""Daily order report."""
import os


def load_orders(path):
    with open(path) as f:
        return [line.strip() for line in f if line.strip()]


def summarize(path):
    orders = load_orders(path)
    if not orders:
        raise ValueError("no orders to report")
    return len(orders)


def main():
    print(summarize(os.environ.get("REPORT_FILE", "orders.txt")))


if __name__ == "__main__":
    main()

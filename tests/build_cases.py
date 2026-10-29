#!/usr/bin/env python3
"""Builds the datasets and expected outputs for the csvsql task.

Not used by the verifier. Run it from anywhere to regenerate:

    tests/data/                         hidden tables
    tests/cases.jsonl                   hidden cases
    environment/workspace/examples/     public tables and cases

Every expected output comes from SQLite (Python's sqlite3 module) running the
same query over the same tables. Each query is also run with
PRAGMA reverse_unordered_selects=ON, and the script stops if the two results
differ, so every case has exactly one correct row order.
"""
import json
import os
import random
import sqlite3
import sys

from queries import EXAMPLE_CASES, HIDDEN_CASES

HERE = os.path.dirname(os.path.abspath(__file__))
TASK = os.path.dirname(HERE)
HIDDEN_DATA = os.path.join(HERE, "data")
EXAMPLES = os.path.join(TASK, "environment", "workspace", "examples")
EXAMPLE_DATA = os.path.join(EXAMPLES, "data")


# ---------------------------------------------------------------- CSV files

def csv_field(v):
    if v is None:
        return ""
    if isinstance(v, float):
        s = repr(v)
    else:
        s = str(v)
    if s == "" or any(ch in s for ch in ',"\r\n'):
        return '"' + s.replace('"', '""') + '"'
    return s


def write_table(directory, filename, columns, rows):
    """columns: list of (name, type). rows: list of tuples."""
    os.makedirs(directory, exist_ok=True)
    with open(os.path.join(directory, filename), "w", encoding="utf-8", newline="") as f:
        f.write(",".join(f"{n}:{t}" for n, t in columns) + "\n")
        for row in rows:
            assert len(row) == len(columns)
            f.write(",".join(csv_field(v) for v in row) + "\n")


def parse_csv(text):
    """The reader described in instruction.md: returns rows of (value, quoted)."""
    rows, row, field, quoted, i, n = [], [], [], False, 0, len(text)
    at_start = True
    while i < n:
        ch = text[i]
        if at_start and ch == '"':
            quoted, at_start, i = True, False, i + 1
            while True:
                if i >= n:
                    raise ValueError("unterminated quote")
                if text[i] == '"':
                    if i + 1 < n and text[i + 1] == '"':
                        field.append('"')
                        i += 2
                        continue
                    i += 1
                    break
                field.append(text[i])
                i += 1
            continue
        if ch == ",":
            row.append(("".join(field), quoted))
            field, quoted, at_start = [], False, True
            i += 1
            continue
        if ch in "\r\n":
            row.append(("".join(field), quoted))
            rows.append(row)
            row, field, quoted, at_start = [], [], False, True
            i += 2 if ch == "\r" and i + 1 < n and text[i + 1] == "\n" else 1
            continue
        field.append(ch)
        at_start = False
        i += 1
    if field or quoted or row:
        row.append(("".join(field), quoted))
        rows.append(row)
    return rows


def load_dir(directory):
    """Loads every NAME.csv in directory into a fresh in-memory SQLite database."""
    conn = sqlite3.connect(":memory:")
    for fn in sorted(os.listdir(directory)):
        if not fn.endswith(".csv"):
            continue
        with open(os.path.join(directory, fn), encoding="utf-8", newline="") as f:
            rows = parse_csv(f.read())
        header = [name for name, _ in rows[0]]
        cols = [h.rsplit(":", 1) for h in header]
        types = [t.upper() for _, t in cols]
        decl = ", ".join(f'"{n}" {t}' for n, t in cols)
        conn.execute(f'CREATE TABLE "{fn[:-4]}" ({decl})')
        data = []
        for r in rows[1:]:
            vals = []
            for (s, quoted), t in zip(r, types):
                if t == "TEXT":
                    vals.append(s if (s or quoted) else None)
                elif s == "":
                    vals.append(None)
                elif t == "INTEGER":
                    vals.append(int(s))
                else:
                    vals.append(float(s))
            data.append(vals)
        conn.executemany(f'INSERT INTO "{fn[:-4]}" VALUES ({",".join("?" * len(cols))})', data)
    return conn


# ---------------------------------------------------------------- datasets

FIRST = ["Abebe", "Ada", "Amara", "Bruno", "Chen", "Dawit", "Elif", "Farah", "Gustav", "Hana",
         "Ines", "Jonas", "Kofi", "Lena", "Mateo", "Nia", "Omar", "Priya", "Rafael", "Selam",
         "Tomas", "Uma", "Viktor", "Wanjiru", "Yusuf", "Zoë", "José", "Liam", "Mekdes", "Sofia"]
LAST = ["Alemu", "Brook", "Costa", "Diallo", "Eriksen", "Fischer", "Girma", "Haile", "Ito", "Jensen",
        "Kamau", "Lopes", "Mendes", "Núñez", "O'Neil", "Petrov", "Quist", "Rossi", "Silva", "Tadesse",
        "Ueda", "Varga", "Weber", "Yilma", "Zeller", "Ångström"]


def date(rng, y0, y1):
    return f"{rng.randint(y0, y1)}-{rng.randint(1, 12):02d}-{rng.randint(1, 28):02d}"


def unique_names(rng, k):
    seen, out = set(), []
    while len(out) < k:
        n = f"{rng.choice(FIRST)} {rng.choice(LAST)}"
        if n not in seen:
            seen.add(n)
            out.append(n)
    return out


def build_hidden_data():
    rng = random.Random(20261029)
    d = HIDDEN_DATA
    if os.path.isdir(d):
        for root, _, files in os.walk(d):
            for fn in files:
                os.remove(os.path.join(root, fn))

    departments = [
        (1, "Engineering", "Berlin", 900000),
        (2, "Sales", "Lisbon", 450000),
        (3, "Marketing", "Lisbon", 300000),
        (4, "Support", "Nairobi", 200000),
        (5, "Finance", None, 250000),
        (6, "Research", "Addis Ababa", 600000),
        (7, "Legal", "Berlin", 150000),
    ]
    write_table(d, "departments.csv",
                [("id", "INTEGER"), ("name", "TEXT"), ("city", "TEXT"), ("budget", "INTEGER")], departments)

    titles = ["Engineer", "Senior Engineer", "Manager", "Analyst", "Designer", "Intern", "Director"]
    names = unique_names(rng, 40)
    employees = []
    for i in range(1, 41):
        dept = None if i in (17, 33) else rng.randint(1, 6)
        salary = rng.randrange(38000, 140000, 250) + rng.choice([0.0, 0.0, 0.0, 0.5, 0.25])
        manager = None if i == 1 or i in (9, 26) else rng.randint(1, min(i - 1, 8))
        bonus = None if rng.random() < 0.3 else rng.randrange(500, 9000, 250) + rng.choice([0.0, 0.5])
        employees.append((i, names[i - 1], dept, rng.choice(titles), float(salary),
                          date(rng, 2012, 2024), manager, bonus))
    write_table(d, "employees.csv",
                [("id", "INTEGER"), ("name", "TEXT"), ("dept_id", "INTEGER"), ("title", "TEXT"),
                 ("salary", "REAL"), ("hired", "TEXT"), ("manager_id", "INTEGER"), ("bonus", "REAL")],
                employees)

    cats = ["tools", "garden", "kitchen", "office", "toys"]
    pnames = ["Hammer", "Trowel", "Kettle", "Stapler", "Kite", "Wrench", "Rake", "Whisk", "Notebook",
              "Yo-yo", "Saw", "Hose", "Ladle", "Marker", "Puzzle", "Drill", "Shovel", "Teapot",
              "Binder", "Robot", "Pliers", "Pruner", "Grater", "Scissors", "Blocks", "Level",
              "Sprinkler", "Colander", "Folder", "Top"]
    products = []
    for i, nm in enumerate(pnames, 1):
        products.append((i, nm, cats[(i - 1) % 5] if i % 7 else rng.choice(cats),
                         rng.randrange(199, 25000) / 100.0, rng.randint(0, 500), 0 if i % 6 == 0 else 1))
    write_table(d, "products.csv",
                [("id", "INTEGER"), ("name", "TEXT"), ("category", "TEXT"), ("price", "REAL"),
                 ("stock", "INTEGER"), ("active", "INTEGER")], products)

    countries = ["ET", "KE", "DE", "PT", "US", "BR"]
    cnames = unique_names(rng, 25)
    customers = []
    for i in range(1, 26):
        nm = cnames[i - 1]
        email = None if i % 5 == 0 else nm.split()[0].lower() + str(i) + "@example.com"
        customers.append((i, nm, None if i == 13 else rng.choice(countries), email, date(rng, 2019, 2024)))
    write_table(d, "customers.csv",
                [("id", "INTEGER"), ("name", "TEXT"), ("country", "TEXT"), ("email", "TEXT"),
                 ("joined", "TEXT")], customers)

    statuses = ["shipped"] * 6 + ["pending"] * 2 + ["cancelled", "returned"]
    orders, items = [], []
    for i in range(1, 91):
        orders.append((i, rng.randint(1, 22), date(rng, 2023, 2024), rng.choice(statuses)))
        if i % 17 == 0:
            continue  # a few orders without items
        for pid in rng.sample(range(1, 31), rng.randint(1, 4)):
            price = products[pid - 1][3]
            disc = None if rng.random() < 0.6 else rng.choice([0.05, 0.1, 0.25])
            items.append((i, pid, rng.randint(1, 10), price, disc))
    write_table(d, "orders.csv",
                [("id", "INTEGER"), ("customer_id", "INTEGER"), ("placed", "TEXT"), ("status", "TEXT")],
                orders)
    write_table(d, "order_items.csv",
                [("order_id", "INTEGER"), ("product_id", "INTEGER"), ("qty", "INTEGER"),
                 ("unit_price", "REAL"), ("discount", "REAL")], items)

    notes = [
        (1, "ana", "plain text"),
        (2, "ben", "has, a comma"),
        (3, "cy", 'she said "hi"'),
        (4, "dee", "two\nlines"),
        (5, "eli", ""),
        (6, "fay", None),
        (7, "gus", "  padded  "),
        (8, "hal", "naïve café ✓"),
        (9, None, "tab\there"),
        (10, "ivy", "crlf\r\ninside"),
        (11, "jo", '"'),
        (12, "kai", "100%_done"),
    ]
    write_table(d, "notes.csv", [("id", "INTEGER"), ("author", "TEXT"), ("body", "TEXT")], notes)

    write_table(d, "Regions.csv", [("Code", "TEXT"), ("Label", "TEXT"), ("Population", "INTEGER")],
                [("ea", "East Africa", 480000000), ("we", "Western Europe", 196000000),
                 ("sa", "South America", 434000000), ("na", "North America", 380000000),
                 ("xx", "Unknown", None)])

    write_table(d, "measures.csv", [("id", "INTEGER"), ("x", "REAL"), ("n", "INTEGER")],
                [(1, 0.1, 7), (2, 0.2, -7), (3, 0.3, 0), (4, 1e-05, 3), (5, 1.5e16, -2),
                 (6, 2.5, 9007199254740993), (7, -0.75, 1), (8, 100.0, 12), (9, None, None),
                 (10, 123456.789, -1), (11, 3.0, 5), (12, 1e300, 2)])

    write_table(os.path.join(d, "alt"), "things.csv", [("id", "INTEGER"), ("label", "TEXT")],
                [(1, "alpha"), (2, "beta"), (3, "gamma")])
    write_table(os.path.join(d, "alt"), "employees.csv", [("id", "INTEGER"), ("name", "TEXT")],
                [(1, "only one")])

    big = random.Random(7)
    regions = ["north", "south", "east", "west", "central"]
    big_orders = [(i, big.randint(1, 5000), big.randrange(100, 50000) / 100.0, big.randint(1, 365),
                   big.choice(regions)) for i in range(1, 50001)]
    write_table(d, "big_orders.csv",
                [("id", "INTEGER"), ("customer_id", "INTEGER"), ("amount", "REAL"), ("day", "INTEGER"),
                 ("region", "TEXT")], big_orders)
    big_items = [(big.randint(1, 52000), f"SKU-{big.randint(1, 2000):04d}", big.randint(1, 10),
                  big.randrange(50, 9999) / 100.0) for _ in range(150000)]
    write_table(d, "big_items.csv",
                [("order_id", "INTEGER"), ("sku", "TEXT"), ("qty", "INTEGER"), ("price", "REAL")], big_items)


def build_example_data():
    rng = random.Random(4242)
    d = EXAMPLE_DATA
    authors = [(1, "Tsegaye Gabre-Medhin", "ET", 1936), (2, "Chimamanda Adichie", "NG", 1977),
               (3, "José Saramago", "PT", 1922), (4, "Ngũgĩ wa Thiong'o", "KE", 1938),
               (5, "Ursula Le Guin", "US", 1929), (6, "Anonymous", None, None)]
    write_table(d, "authors.csv", [("id", "INTEGER"), ("name", "TEXT"), ("country", "TEXT"),
                                   ("born", "INTEGER")], authors)
    genres = ["novel", "poetry", "drama", "essay"]
    books = []
    for i in range(1, 19):
        books.append((i, f"Book {chr(64 + i)}", rng.randint(1, 5) if i != 18 else None, rng.choice(genres),
                      rng.randint(80, 900), rng.randrange(500, 4000) / 100.0 if i % 5 else None))
    write_table(d, "books.csv", [("id", "INTEGER"), ("title", "TEXT"), ("author_id", "INTEGER"),
                                 ("genre", "TEXT"), ("pages", "INTEGER"), ("price", "REAL")], books)
    members = [(i, n, date(rng, 2018, 2024)) for i, n in
               enumerate(["Abel", "Bethlehem", "Carla", "Dagmar", "Eyob", "Fatuma", "Gil", "Hiwot"], 1)]
    write_table(d, "members.csv", [("id", "INTEGER"), ("name", "TEXT"), ("since", "TEXT")], members)
    loans = []
    for i in range(1, 31):
        loans.append((i, rng.randint(1, 18), rng.randint(1, 7), date(rng, 2024, 2024),
                      None if rng.random() < 0.3 else rng.randint(1, 40)))
    write_table(d, "loans.csv", [("id", "INTEGER"), ("book_id", "INTEGER"), ("member_id", "INTEGER"),
                                 ("out", "TEXT"), ("days", "INTEGER")], loans)
    write_table(d, "quotes.csv", [("id", "INTEGER"), ("text", "TEXT")],
                [(1, "short"), (2, "with, comma"), (3, 'with "quotes"'), (4, "multi\nline"), (5, ""),
                 (6, None)])
    write_table(os.path.join(d, "extra"), "colors.csv", [("id", "INTEGER"), ("color", "TEXT")],
                [(1, "red"), (2, "green")])


# ---------------------------------------------------------------- expected outputs

def fmt_value(v):
    if v is None:
        return None
    if isinstance(v, float):
        return repr(v)
    if isinstance(v, bytes):
        raise ValueError("blob in result")
    return str(v)


def out_field(s):
    if s is None:
        return ""
    if s == "" or any(ch in s for ch in ',"\r\n'):
        return '"' + s.replace('"', '""') + '"'
    return s


def run_query(conn, sql, reverse):
    conn.execute(f"PRAGMA reverse_unordered_selects = {1 if reverse else 0}")
    try:
        cur = conn.execute(sql)
        rows = cur.fetchall()
    except (sqlite3.Error, sqlite3.Warning):
        return None
    if cur.description is None:
        return None
    lines = [",".join(out_field(d[0]) for d in cur.description)]
    for r in rows:
        lines.append(",".join(out_field(fmt_value(v)) for v in r))
    return "".join(line + "\n" for line in lines)


def expected(conns, data_dir, args, cid):
    """Runs a command line through SQLite and returns (stdout, exit_code)."""
    sub, sql = "", None
    rest = list(args)
    while rest:
        a = rest.pop(0)
        if a == "-d":
            sub = rest.pop(0)
        else:
            sql = a
    path = os.path.normpath(os.path.join(data_dir, sub))
    if path not in conns:
        conns[path] = load_dir(path)
    conn = conns[path]
    a = run_query(conn, sql, False)
    b = run_query(conn, sql, True)
    if a != b:
        sys.exit(f"{cid}: row order is not fully determined:\n{sql}")
    if a is not None and len(a) > 200000:
        sys.exit(f"{cid}: output too large")
    return (a, 0) if a is not None else ("", 1)


def make_cases(specs, data_dir, prefix):
    conns = {}
    out = []
    for n, spec in enumerate(specs, 1):
        cid = f"{prefix}{n:03d}"
        if "expect" in spec:
            stdout, code = spec["expect"]
        else:
            stdout, code = expected(conns, data_dir, spec["args"], cid)
        case = dict(id=cid, category=spec["cat"], args=spec["args"], stdout=stdout, exit_code=code)
        if code != 0 and "control" not in spec:
            sys.exit(f"{cid}: a failing case needs a control query")
        if "control" in spec:
            c_out, c_code = expected(conns, data_dir, spec["control"], cid)
            if code == 0 or c_code != 0:
                sys.exit(f"{cid}: the case must fail and its control must succeed")
            case["control"] = dict(args=spec["control"], stdout=c_out, exit_code=0)
        out.append(case)
    return out


def write_cases(path, cases):
    with open(path, "w", encoding="utf-8") as f:
        for c in cases:
            f.write(json.dumps(c, ensure_ascii=False) + "\n")


def main():
    build_hidden_data()
    build_example_data()
    hidden = make_cases(HIDDEN_CASES, HIDDEN_DATA, "h")
    examples = make_cases(EXAMPLE_CASES, EXAMPLE_DATA, "e")
    write_cases(os.path.join(HERE, "cases.jsonl"), hidden)
    write_cases(os.path.join(EXAMPLES, "cases.jsonl"), examples)
    errs = sum(c["exit_code"] != 0 for c in hidden)
    print(f"hidden: {len(hidden)} cases ({errs} expect an error), examples: {len(examples)} cases")


if __name__ == "__main__":
    main()

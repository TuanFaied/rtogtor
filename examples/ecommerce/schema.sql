-- RtoGtoR e-commerce example (PostgreSQL).
-- Exercises: single and composite primary keys, foreign keys, and two
-- self-references (customers.referred_by, products.parent_product_id).
-- Load only into a disposable database.

CREATE TABLE customers (
    id          bigint      PRIMARY KEY,
    email       text        NOT NULL UNIQUE,
    full_name   text        NOT NULL,
    referred_by bigint      REFERENCES customers (id),
    created_at  timestamptz NOT NULL
);

CREATE TABLE products (
    id                bigint        PRIMARY KEY,
    sku               text          NOT NULL UNIQUE,
    name              text          NOT NULL,
    price             numeric(12,2) NOT NULL,
    parent_product_id bigint        REFERENCES products (id)
);

CREATE TABLE orders (
    id          bigint      PRIMARY KEY,
    customer_id bigint      NOT NULL REFERENCES customers (id),
    status      text        NOT NULL,
    placed_at   timestamptz NOT NULL
);

CREATE TABLE order_items (
    order_id   bigint        NOT NULL REFERENCES orders (id),
    line_no    integer       NOT NULL,
    product_id bigint        NOT NULL REFERENCES products (id),
    quantity   integer       NOT NULL CHECK (quantity > 0),
    unit_price numeric(12,2) NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

-- Sample data
INSERT INTO customers (id, email, full_name, referred_by, created_at) VALUES
    (1, 'ada@example.com',   'Ada Lovelace',    NULL, '2026-01-05T09:00:00Z'),
    (2, 'alan@example.com',  'Alan Turing',     1,    '2026-02-11T14:30:00Z'),
    (3, 'grace@example.com', 'Grace Hopper',    2,    '2026-03-20T08:15:00Z');

INSERT INTO products (id, sku, name, price, parent_product_id) VALUES
    (10, 'TEE',      'T-shirt',          19.90, NULL),
    (11, 'TEE-RED',  'T-shirt (red)',    19.90, 10),
    (12, 'TEE-BLUE', 'T-shirt (blue)',   21.50, 10),
    (20, 'MUG',      'Mug',               9.00, NULL);

INSERT INTO orders (id, customer_id, status, placed_at) VALUES
    (1001, 2, 'shipped', '2026-04-01T10:00:00Z'),
    (1002, 3, 'pending', '2026-04-02T16:45:00Z');

INSERT INTO order_items (order_id, line_no, product_id, quantity, unit_price) VALUES
    (1001, 1, 11, 2, 19.90),
    (1001, 2, 20, 1,  9.00),
    (1002, 1, 12, 1, 21.50);

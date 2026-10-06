CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    reset_token TEXT,
    reset_token_expires_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS contacts (
    id BIGSERIAL PRIMARY KEY,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100),
    email VARCHAR(255),
    mobile VARCHAR(30),
    alternate_mobile VARCHAR(30),
    company_id BIGINT,
    lead_status_id BIGINT,
    lead_owner_id BIGINT REFERENCES users(id),
    destination VARCHAR(150),
    source VARCHAR(100),
    notes TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS deal_stages (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS deals (
    id BIGSERIAL PRIMARY KEY,
    title VARCHAR(200) NOT NULL,
    amount NUMERIC(15,2) DEFAULT 0,
    stage_id BIGINT REFERENCES deal_stages(id),
    owner_id BIGINT REFERENCES users(id),
    contact_id BIGINT REFERENCES contacts(id),
    monthly_recurring_revenue NUMERIC(15,2) DEFAULT 0,
    annual_recurring_revenue NUMERIC(15,2) DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO deal_stages (name)
VALUES
    ('Qualification'),
    ('Needs Analysis'),
    ('Proposal'),
    ('Negotiation'),
    ('Closed Won'),
    ('Closed Lost')
ON CONFLICT (name) DO NOTHING;
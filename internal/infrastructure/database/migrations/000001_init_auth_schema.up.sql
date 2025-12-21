CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) NOT NULL UNIQUE, 
    password_hash VARCHAR(255) NOT NULL, 
    full_name VARCHAR(100),
    avatar_url TEXT,                     
    role VARCHAR(50) DEFAULT 'user', 
    
    is_active BOOLEAN DEFAULT TRUE,      
    is_verified BOOLEAN DEFAULT FALSE,   
    mfa_enabled BOOLEAN DEFAULT FALSE,   
    mfa_secret VARCHAR(255),            
   
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE user_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash VARCHAR(255) NOT NULL, 
    user_agent TEXT,       
    client_ip VARCHAR(45), 
    is_blocked BOOLEAN DEFAULT FALSE, 
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sessions_user_id ON user_sessions(user_id);

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,        
    prefix VARCHAR(10) NOT NULL,       
    key_hash VARCHAR(255) NOT NULL,    
    scopes JSONB DEFAULT '[]',         
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,            
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_api_keys_prefix ON api_keys(prefix); 

CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    slug VARCHAR(100) NOT NULL UNIQUE, 
    description TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(50) NOT NULL UNIQUE,  
    description TEXT,
    is_system BOOLEAN DEFAULT FALSE,   
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE role_permissions (
    role_id UUID REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE projects (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL, 
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE project_members (
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID REFERENCES roles(id) ON DELETE RESTRICT, 
    
    joined_at TIMESTAMPTZ DEFAULT NOW(),
    
    PRIMARY KEY (project_id, user_id) 
);

CREATE INDEX idx_project_members_user_id ON project_members(user_id);
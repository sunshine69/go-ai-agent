"""
Auth router - handles user registration, login, and session management
"""

from fastapi import APIRouter, HTTPException, Depends
from pydantic import BaseModel, EmailStr
from passlib.context import CryptContext
from datetime import datetime, timedelta

# In-memory user storage for demo
users_db = {}

router = APIRouter()

pwd_context = CryptContext(schemes=["bcrypt"], deprecated="auto")

class UserRegister(BaseModel):
    username: str
    email: str
    password: str

class UserLogin(BaseModel):
    username: str
    password: str

class UserResponse(BaseModel):
    user_id: str
    username: str
    email: str

class TokenResponse(BaseModel):
    access_token: str
    token_type: str

@router.post("/register", response_model=UserResponse)
async def register(user: UserRegister):
    # Check if user already exists
    if user.username in users_db:
        raise HTTPException(status_code=400, detail="Username already exists")
    
    user_id = f"USR-{len(users_db) + 1:04d}"
    hashed_password = pwd_context.hash(user.password)
    
    users_db[user.username] = {
        "user_id": user_id,
        "username": user.username,
        "email": user.email,
        "hashed_password": hashed_password,
        "created_at": datetime.now().isoformat(),
    }
    
    return {"user_id": user_id, "username": user.username, "email": user.email}

@router.post("/login", response_model=TokenResponse)
async def login(user: UserLogin):
    # Check if user exists
    if user.username not in users_db:
        raise HTTPException(status_code=401, detail="Invalid credentials")
    
    # Check password
    db_user = users_db[user.username]
    if not pwd_context.verify(user.password, db_user["hashed_password"]):
        raise HTTPException(status_code=401, detail="Invalid credentials")
    
    # Generate token (simple demo - in production, use JWT)
    token = f"token-{db_user['user_id']}-{datetime.now().isoformat()}"
    
    return {
        "access_token": token,
        "token_type": "bearer",
    }

@router.get("/me")
async def get_current_user():
    # Demo - return first user
    if users_db:
        username = list(users_db.keys())[0]
        return {
            "user_id": users_db[username]["user_id"],
            "username": users_db[username]["username"],
            "email": users_db[username]["email"],
        }
    raise HTTPException(status_code=404, detail="No users found")

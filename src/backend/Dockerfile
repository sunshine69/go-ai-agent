# Backend Dockerfile
FROM python:3.11-slim

WORKDIR /app

# Install dependencies
COPY src/backend/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY src/backend/ ./

# Set environment variables
ENV PYTHONUNBUFFERED=1

# Create directories
RUN mkdir -p resources/documents

# Expose port
EXPOSE 8000

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]

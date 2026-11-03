FROM python:3.12.13-alpine3.24
RUN apk add --no-cache bash openssl coreutils util-linux curl git openssh nginx \
    && ln -s /usr/local/bin/python3 /usr/bin/python3
ENV PYTHONDONTWRITEBYTECODE=1
WORKDIR /src
ENTRYPOINT ["python3"]

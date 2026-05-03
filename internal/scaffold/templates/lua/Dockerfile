FROM openresty/openresty:1.25-alpine
WORKDIR /app
COPY public/ /usr/local/openresty/nginx/html/
COPY nginx.conf /usr/local/openresty/nginx/conf/nginx.conf
EXPOSE 8080
CMD ["openresty", "-g", "daemon off;"]

FROM openjdk:21-jdk-slim
WORKDIR /minecraft
COPY mcpackctl /usr/local/bin/mcpackctl
RUN chmod +x /usr/local/bin/mcpackctl
EXPOSE 25565
ENTRYPOINT ["mcpackctl"]

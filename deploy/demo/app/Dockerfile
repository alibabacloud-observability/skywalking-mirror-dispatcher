# Build the SkyWalking-instrumented demo app.
# All external coordinates are build args so the image builds in any region.
#
#   SW_AGENT_VERSION  SkyWalking Java agent version (default: latest published)
#   SW_AGENT_URL      Full agent tarball URL. Default is the Apache CDN, which
#                     only keeps the latest release. For pinned/older versions use
#                     https://archive.apache.org/dist/skywalking/java-agent/... or a
#                     regional mirror, e.g.
#                       --build-arg SW_AGENT_URL=https://mirrors.tuna.tsinghua.edu.cn/apache/skywalking/java-agent/9.6.0/apache-skywalking-java-agent-9.6.0.tgz
#   MAVEN_IMAGE/JRE_IMAGE  swap in registry-mirror-prefixed images if docker.io is blocked.

ARG MAVEN_IMAGE=maven:3.9-eclipse-temurin-17
ARG JRE_IMAGE=eclipse-temurin:17-jre

FROM ${MAVEN_IMAGE} AS build
WORKDIR /src
COPY settings.xml /root/.m2/settings.xml
COPY pom.xml .
RUN mvn -B -s /root/.m2/settings.xml dependency:go-offline
COPY src ./src
RUN mvn -B -s /root/.m2/settings.xml clean package -DskipTests

# Download the agent with curl (not Dockerfile ADD): some mirrors reject the
# BuildKit ADD user-agent with HTTP 403.
FROM curlimages/curl:8.10.1 AS agent
ARG SW_AGENT_VERSION=9.6.0
ARG SW_AGENT_URL=https://dlcdn.apache.org/skywalking/java-agent/${SW_AGENT_VERSION}/apache-skywalking-java-agent-${SW_AGENT_VERSION}.tgz
RUN curl -fsSL -o /tmp/agent.tgz "${SW_AGENT_URL}"

FROM ${JRE_IMAGE}
WORKDIR /app
COPY --from=agent /tmp/agent.tgz /tmp/agent.tgz
RUN tar xzf /tmp/agent.tgz -C /opt && rm /tmp/agent.tgz
COPY --from=build /src/target/sw-demo.jar /app/app.jar
EXPOSE 8080
ENTRYPOINT ["java","-javaagent:/opt/skywalking-agent/skywalking-agent.jar","-jar","/app/app.jar"]

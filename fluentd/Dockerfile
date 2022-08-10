# fluentd/Dockerfile
#FROM fluent/fluentd:v0.12-debian
#RUN ["gem", "install", "fluent-plugin-elasticsearch", "--no-rdoc", "--no-ri", "--version", "1.9.2"]
#FROM fluent/fluentd:v1.12-debian
#USER root
#RUN ["gem", "install", "fluent-plugin-elasticsearch"]
#RUN ["gem", "install", "fluent-plugin-input-gelf"]
#USER fluent

#FROM fluent/fluentd:v1.12.0-debian-1.0
#USER root
#RUN sudo gem install elasticsearch -v 7.13.3
#RUN ["gem", "install", "fluent-plugin-elasticsearch", "--no-document", "--version", "5.0.3"]
#USER fluent

# Inherit from the official fluentd image.
FROM fluent/fluentd:v1.13-1

# Install elasticsearch library and fluent plugin.
USER root
RUN apk add --no-cache --update --virtual .build-deps \
        sudo build-base ruby-dev \
 # Pin the elasticsearch-ruby library < 7.14.
 # See https://github.com/uken/fluent-plugin-elasticsearch/issues/912
 && sudo gem install elasticsearch -v 7.13.3 \
 && sudo gem install fluent-plugin-elasticsearch \
 && sudo gem sources --clear-all \
 && apk del .build-deps \
 && rm -rf /tmp/* /var/tmp/* /usr/lib/ruby/gems/*/cache/*.gem
#RUN ["gem", "install", "fluent-plugin-elasticsearch", "--no-document", "--version", "5.0.5"]
USER fluent
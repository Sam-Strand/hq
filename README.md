manager='dnf' # apt dnf 
extension='rpm' # rpm deb apk
version='1.1.0' # apt dnf 
curl -sL https://github.com/Sam-Strand/hq/releases/download/v$version/hq_${version}_linux_amd64.$extension -o /tmp/hq.$extension &&
sudo $manager install -y /tmp/hq.$extension &&
rm /tmp/hq.$extension

manager='dnf' # apt dnf 
extension='rpm' # rpm deb apk
curl -sL https://github.com/Sam-Strand/hq/releases/download/v0.1.1/hq_0.1.1_linux_amd64.$extension -o /tmp/hq.$extension &&
sudo $manager install -y /tmp/hq.$extension &&
rm /tmp/hq.$extension

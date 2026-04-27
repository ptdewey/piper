{
  lib,
  buildGoModule,
  tailwindcss_4,
  fetchFromGitHub,
  source ? fetchFromGitHub {
    owner = "ptdewey";
    repo = "piper";
    rev = "3ffedcb9c96f1cd4b2a98469c6475561e863a44a";
    hash = "sha256-spivSgVycIEd3hq0L11ye2msiCxZb8JyHLZhPYlmPlg=";
  },
}:
buildGoModule {
  pname = "tealfm-piper";
  version = "0.0.4";

  src = source;

  vendorHash = "sha256-0CAKzBBARoHSqDv34Xx3Yek6r33Exhrhvn+FzGlby14=";

  nativeBuildInputs = [ tailwindcss_4 ];

  env.CGO_ENABLED = 1;

  subPackages = [ "cmd" ];

  ldflags = [
    "-s"
    "-w"
  ];

  postBuild = ''
    cp -r ./pages/templates $out/
    cp -r ./pages/static $out/
    tailwindcss -i $out/static/base.css -o $out/static/main.css -m
  '';

  postInstall = ''
    mv $out/bin/cmd $out/bin/piper
  '';

  meta = with lib; {
    description = "Music scrobbler service for teal.fm";
    homepage = "https://github.com/teal-fm/piper";
    license = licenses.mit;
    maintainers = with maintainers; [ ptdewey ];
    mainProgram = "piper";
  };
}
